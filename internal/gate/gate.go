package gate

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/k8s"
)

// Gate listens on one local port per playit tunnel. Each port belongs to exactly
// one server: whatever hostname a client sends, the handshake is rewritten to that
// server's private name and handed to mc-router, so a tunnel can only reach its server.
type Gate struct {
	bind        string
	upstream    string
	dialTimeout time.Duration
	hsTimeout   time.Duration
	slots       chan struct{}

	mu        sync.Mutex
	listeners map[int]*listener
	// ctx ends listeners and dials on shutdown; set by Run.
	ctx context.Context
}

type listener struct {
	l    net.Listener
	mu   sync.RWMutex
	host string
}

func (l *listener) target() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.host
}

// New creates a gate binding ports on bind (usually 127.0.0.1) and forwarding to upstream.
func New(bind, upstream string, maxConns int) *Gate {
	return &Gate{bind: bind, upstream: upstream, dialTimeout: 5 * time.Second, hsTimeout: 5 * time.Second,
		slots: make(chan struct{}, maxConns), listeners: map[int]*listener{}, ctx: context.Background()}
}

// Ports returns the open ports and their servers.
func (g *Gate) Ports() map[int]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[int]string, len(g.listeners))
	for p, l := range g.listeners {
		out[p] = l.target()
	}
	return out
}

// Sync opens, retargets and closes listeners to match routes (port -> private host).
func (g *Gate) Sync(routes map[int]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for port, l := range g.listeners {
		if _, keep := routes[port]; !keep {
			_ = l.l.Close()
			delete(g.listeners, port)
			slog.Info("gate: port closed", "port", port)
		}
	}
	for port, host := range routes {
		if l, ok := g.listeners[port]; ok {
			l.mu.Lock()
			l.host = host
			l.mu.Unlock()
			continue
		}
		var lc net.ListenConfig
		nl, err := lc.Listen(g.ctx, "tcp", net.JoinHostPort(g.bind, strconv.Itoa(port)))
		if err != nil {
			slog.Warn("gate: cannot listen", "port", port, "err", err)
			continue
		}
		l := &listener{l: nl, host: host}
		g.listeners[port] = l
		slog.Info("gate: port open", "port", port, "server", host)
		go g.accept(l)
	}
}

// Close shuts every listener.
func (g *Gate) Close() { g.Sync(map[int]string{}) }

func (g *Gate) accept(l *listener) {
	for {
		c, err := l.l.Accept()
		if err != nil {
			return // closed by Sync
		}
		select {
		case g.slots <- struct{}{}:
		default:
			// Too many open connections: refuse instead of queueing.
			_ = c.Close()
			continue
		}
		go func() {
			defer func() { <-g.slots }()
			g.handle(c, l.target())
		}()
	}
}

func (g *Gate) handle(c net.Conn, private string) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(g.hsTimeout))
	br := bufio.NewReaderSize(c, 2048)
	hs, err := ReadHandshake(br)
	if err != nil {
		slog.Debug("gate: rejected connection", "err", err)
		return
	}
	_ = c.SetReadDeadline(time.Time{})

	d := net.Dialer{Timeout: g.dialTimeout}
	up, err := d.DialContext(g.ctx, "tcp", g.upstream)
	if err != nil {
		slog.Warn("gate: upstream unreachable", "err", err)
		return
	}
	defer up.Close()

	hs.Host = private
	if _, err := up.Write(hs.Encode()); err != nil {
		return
	}
	// Bytes after the handshake that were already read go first.
	if n := br.Buffered(); n > 0 {
		rest, _ := br.Peek(n)
		if _, err := up.Write(rest); err != nil {
			return
		}
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); closeWrite(up); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); closeWrite(c); done <- struct{}{} }()
	<-done
	<-done
}

func closeWrite(c net.Conn) {
	if t, ok := c.(*net.TCPConn); ok {
		_ = t.CloseWrite()
	}
}

// RoutesFromServices maps each playit port to the private name of its server.
func RoutesFromServices(ctx context.Context, cs kubernetes.Interface, namespace string) (map[int]string, error) {
	list, err := cs.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{LabelSelector: k8s.LabelPlayit + "=true"})
	if err != nil {
		return nil, err
	}
	out := make(map[int]string, len(list.Items))
	dup := map[int]bool{}
	for _, s := range list.Items {
		port, err := strconv.Atoi(s.Annotations[k8s.AnnotationPlayitPort])
		host := strings.TrimSpace(strings.Split(s.Annotations[k8s.AnnotationRouterHost], ",")[0])
		if err != nil || port < 1024 || port > 65535 || host == "" {
			continue
		}
		if _, seen := out[port]; seen || dup[port] {
			// The manager never sets one port twice; refuse to guess if it happens.
			slog.Warn("gate: port claimed by two servers, skipping it", "port", port)
			delete(out, port)
			dup[port] = true
			continue
		}
		out[port] = strings.ToLower(host)
	}
	return out, nil
}

// Run keeps the listeners in sync with the Services until ctx ends.
func (g *Gate) Run(ctx context.Context, cs kubernetes.Interface, namespace string, every time.Duration) {
	g.mu.Lock()
	g.ctx = ctx
	g.mu.Unlock()
	defer g.Close()
	for {
		r, err := RoutesFromServices(ctx, cs, namespace)
		if err != nil {
			slog.Warn("gate: listing servers failed, keeping the open ports", "err", err)
		} else {
			g.Sync(r)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
