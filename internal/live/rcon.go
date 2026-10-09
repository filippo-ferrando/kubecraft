// Package live talks to running servers: RCON commands, player lists, and resource usage.
package live

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorcon/rcon"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/metrics"
)

// ErrUnavailable means RCON could not be reached, usually because the server is starting.
var ErrUnavailable = errors.New("RCON is not available, the server may still be starting")

const MaxCommandLen = 1000

// PasswordFunc returns the RCON password of a server.
type PasswordFunc func(ctx context.Context, slug string) (string, error)

// AddrFunc returns the RCON address of a server.
type AddrFunc func(slug string) string

// Pool keeps one RCON connection per server. Commands to the same server are serialized.
type Pool struct {
	addr     AddrFunc
	password PasswordFunc
	timeout  time.Duration

	mu    sync.Mutex
	conns map[string]*pooled
}

type pooled struct {
	mu   sync.Mutex
	conn *rcon.Conn
}

func NewPool(addr AddrFunc, password PasswordFunc) *Pool {
	return &Pool{addr: addr, password: password, timeout: 5 * time.Second, conns: map[string]*pooled{}}
}

func (p *Pool) entry(slug string) *pooled {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.conns[slug]
	if !ok {
		e = &pooled{}
		p.conns[slug] = e
	}
	return e
}

// Exec runs a command and returns its output without color codes.
// A stale connection is replaced once before giving up.
func (p *Pool) Exec(ctx context.Context, slug, cmd string) (string, error) {
	out, err := p.exec(ctx, slug, cmd)
	switch {
	case err == nil:
		metrics.RCONCommands.WithLabelValues("ok").Inc()
	case errors.Is(err, ErrUnavailable):
		metrics.RCONCommands.WithLabelValues("unavailable").Inc()
	default:
		metrics.RCONCommands.WithLabelValues("rejected").Inc()
	}
	return out, err
}

func (p *Pool) exec(ctx context.Context, slug, cmd string) (string, error) {
	cmd = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cmd), "/"))
	if cmd == "" {
		return "", errors.New("empty command")
	}
	if len(cmd) > MaxCommandLen || strings.ContainsAny(cmd, "\r\n\x00") {
		return "", fmt.Errorf("command must be one line of at most %d characters", MaxCommandLen)
	}
	e := p.entry(slug)
	e.mu.Lock()
	defer e.mu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		fresh := false
		if e.conn == nil {
			pw, err := p.password(ctx, slug)
			if err != nil {
				return "", ErrUnavailable
			}
			e.conn, err = rcon.Dial(p.addr(slug), pw, rcon.SetDialTimeout(p.timeout), rcon.SetDeadline(p.timeout))
			if err != nil {
				e.conn = nil
				return "", ErrUnavailable
			}
			fresh = true
		}
		out, err := e.conn.Execute(cmd)
		if err == nil {
			return StripColors(out), nil
		}
		_ = e.conn.Close()
		e.conn = nil
		if fresh {
			return "", ErrUnavailable
		}
	}
	return "", ErrUnavailable
}

// Forget closes the connection of a server, for example after it stopped.
func (p *Pool) Forget(slug string) {
	p.mu.Lock()
	e, ok := p.conns[slug]
	delete(p.conns, slug)
	p.mu.Unlock()
	if ok {
		e.mu.Lock()
		if e.conn != nil {
			_ = e.conn.Close()
		}
		e.mu.Unlock()
	}
}

var colorRe = regexp.MustCompile(`§[0-9a-fk-orA-FK-OR]`)

// StripColors removes Minecraft formatting codes.
func StripColors(s string) string { return colorRe.ReplaceAllString(s, "") }

// "There are 1 of a max of 20 players online: Steve" (vanilla) or "There are 1/20 players online:" (some forks).
var listRe = regexp.MustCompile(`(?i)there are (\d+)(?: of a max(?: of)? |/)(\d+) players online:?\s*(.*)`)

// Players is the parsed output of the list command.
type Players struct {
	Online int      `json:"online"`
	Max    int      `json:"max"`
	Names  []string `json:"names"`
}

// ParseList parses the output of the list command.
func ParseList(out string) (Players, bool) {
	m := listRe.FindStringSubmatch(strings.TrimSpace(StripColors(out)))
	if m == nil {
		return Players{}, false
	}
	var p Players
	_, _ = fmt.Sscan(m[1], &p.Online)
	_, _ = fmt.Sscan(m[2], &p.Max)
	p.Names = []string{}
	for _, n := range strings.Split(m[3], ",") {
		// Some forks prefix names with a group, like "default: Steve".
		if i := strings.LastIndex(n, ":"); i >= 0 {
			n = n[i+1:]
		}
		if n = strings.TrimSpace(n); n != "" {
			p.Names = append(p.Names, n)
		}
	}
	return p, true
}

// Close drops every connection.
func (p *Pool) Close() {
	p.mu.Lock()
	slugs := make([]string, 0, len(p.conns))
	for s := range p.conns {
		slugs = append(slugs, s)
	}
	p.mu.Unlock()
	for _, s := range slugs {
		p.Forget(s)
	}
}
