package api

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/metrics"
)

const (
	logBacklog     = 200
	wsWriteTimeout = 10 * time.Second
	maxLogLine     = 64 * 1024
)

// termMsg is a message on the logs and console sockets.
type termMsg struct {
	Type    string `json:"type"` // log, output, status, error (server) or command (client)
	Line    string `json:"line,omitempty"`
	Command string `json:"command,omitempty"`
	Text    string `json:"text,omitempty"`
}

func (s *Server) logsWS(w http.ResponseWriter, r *http.Request)    { s.terminal(w, r, false) }
func (s *Server) consoleWS(w http.ResponseWriter, r *http.Request) { s.terminal(w, r, true) }

// originHosts are the hosts allowed as WebSocket origins.
func (s *Server) originHosts() []string {
	hosts := make([]string, 0, len(s.origins))
	for _, o := range s.origins {
		if u, err := url.Parse(o); err == nil && u.Host != "" {
			hosts = append(hosts, u.Host)
		}
	}
	return hosts
}

// terminal streams the server log and, for the console, runs commands over RCON.
func (s *Server) terminal(w http.ResponseWriter, r *http.Request, console bool) {
	srv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	id := mustIdentity(r)
	if console && !id.IsAdmin() && !id.User.ConsoleEnabled {
		writeError(w, http.StatusForbidden, "the console is disabled for your account")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.originHosts()})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	kind := "logs"
	if console {
		kind = "console"
	}
	metrics.WebSockets.WithLabelValues(kind).Inc()
	defer metrics.WebSockets.WithLabelValues(kind).Dec()
	conn.SetReadLimit(4096)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	out := make(chan termMsg, 256)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-out:
				wctx, wcancel := context.WithTimeout(ctx, wsWriteTimeout)
				err := wsjson.Write(wctx, conn, m)
				wcancel()
				if err != nil {
					// Slow or gone client: drop it rather than buffer forever.
					return
				}
			}
		}
	}()
	send := func(m termMsg) bool {
		select {
		case out <- m:
			return true
		case <-ctx.Done():
			return false
		}
	}

	go s.followLogs(ctx, srv.Slug, send)

	if !console {
		// Only control frames are expected; this returns when the client goes away.
		_, _, _ = conn.Read(ctx)
		cancel()
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return
	}
	s.readCommands(ctx, conn, r, srv, send)
	cancel()
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func (s *Server) readCommands(ctx context.Context, conn *websocket.Conn, r *http.Request, srv sqlc.Server, send func(termMsg) bool) {
	last := time.Time{}
	for {
		var m termMsg
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			return
		}
		if m.Type != "command" {
			continue
		}
		cmd := strings.TrimSpace(m.Command)
		if cmd == "" {
			continue
		}
		// Simple flood guard: at most one command every 100ms.
		if time.Since(last) < 100*time.Millisecond {
			send(termMsg{Type: "error", Text: "slow down"})
			continue
		}
		last = time.Now()
		s.auditAs(r, "console.command", srv.Slug, map[string]any{"command": cmd})
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		res, err := s.rcon.Exec(cctx, srv.Slug, cmd)
		cancel()
		result := "ok"
		if err != nil {
			result = "error"
		}
		metrics.ConsoleCommands.WithLabelValues(result).Inc()
		if err != nil {
			if !send(termMsg{Type: "error", Command: cmd, Text: err.Error()}) {
				return
			}
			continue
		}
		if !send(termMsg{Type: "output", Command: cmd, Text: res}) {
			return
		}
	}
}

// followLogs streams the pod log and re-attaches after restarts. Lines already
// sent are skipped using their timestamps.
func (s *Server) followLogs(ctx context.Context, slug string, send func(termMsg) bool) {
	kube := s.mgr.Kube()
	var lastTS time.Time
	lastStatus := ""
	status := func(text string) bool {
		if text == lastStatus {
			return true
		}
		lastStatus = text
		return send(termMsg{Type: "status", Text: text})
	}
	wait := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}

	for ctx.Err() == nil {
		started, err := kube.ContainerStarted(ctx, slug)
		if err != nil || !started {
			msg := "waiting for the server to start"
			if apierrors.IsNotFound(err) {
				msg = "server is not running"
			}
			if !status(msg) || !wait(3*time.Second) {
				return
			}
			continue
		}

		var tail *int64
		var since *metav1.Time
		if lastTS.IsZero() {
			n := int64(logBacklog)
			tail = &n
		} else {
			t := metav1.NewTime(lastTS)
			since = &t
		}
		stream, err := kube.LogStream(ctx, slug, tail, since)
		if err != nil {
			slog.Debug("log stream failed", "server", slug, "err", err)
			if !wait(3 * time.Second) {
				return
			}
			continue
		}
		if !status("connected") {
			_ = stream.Close()
			return
		}
		sc := bufio.NewScanner(stream)
		sc.Buffer(make([]byte, 0, 4096), maxLogLine)
		for sc.Scan() {
			ts, line := splitTimestamp(sc.Text())
			if !ts.IsZero() {
				if !ts.After(lastTS) {
					continue
				}
				lastTS = ts
			}
			if !send(termMsg{Type: "log", Line: line}) {
				_ = stream.Close()
				return
			}
		}
		_ = stream.Close()
		if ctx.Err() != nil {
			return
		}
		lastStatus = ""
		if !status("log stream ended, reconnecting") || !wait(2*time.Second) {
			return
		}
	}
}

// splitTimestamp splits "2024-01-01T00:00:00.123456789Z line" into time and line.
func splitTimestamp(s string) (time.Time, string) {
	i := strings.IndexByte(s, ' ')
	if i <= 0 {
		return time.Time{}, s
	}
	t, err := time.Parse(time.RFC3339Nano, s[:i])
	if err != nil {
		return time.Time{}, s
	}
	return t, s[i+1:]
}
