// Package playit reads the playit.gg tunnels of the agent. Tunnels are created by
// hand in the playit dashboard (agent keys are read only); a tunnel named
// kubecraft-<slug> belongs to that server, and its local port says where the
// gateway must listen for it.
package playit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/metrics"
)

const (
	DefaultAPI = "https://api.playit.gg"
	// Prefix of tunnel names kubecraft picks up: kubecraft-<slug>.
	Prefix = "kubecraft-"
	// LocalIP is where every kubecraft tunnel must point; the gateway shares the agent's pod.
	LocalIP = "127.0.0.1"
)

// Tunnel is what kubecraft needs to know about one tunnel.
type Tunnel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"-"`
	Address   string `json:"address"`
	LocalIP   string `json:"local_ip"`
	LocalPort int    `json:"local_port"`
	Online    bool   `json:"online"`
	// Routable means the gateway can serve it: right local address, valid and unshared port.
	Routable bool     `json:"routable"`
	Problems []string `json:"problems"`
}

// Client lists tunnels with the agent key.
type Client struct {
	api  string
	key  string
	http *http.Client
}

func NewClient(api, key string) *Client {
	return &Client{api: strings.TrimSuffix(api, "/"), key: strings.TrimSpace(key), http: &http.Client{Timeout: 15 * time.Second}}
}

type rawTunnel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	UserEnabled   bool     `json:"user_enabled"`
	OfflineReason []string `json:"offline_reasons"`
	TunnelType    string   `json:"tunnel_type"`
	Origin        struct {
		Type    string `json:"type"`
		Details struct {
			ConfigData struct {
				Fields []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"fields"`
			} `json:"config_data"`
		} `json:"details"`
	} `json:"origin"`
	ConnectAddresses []struct {
		Type  string `json:"type"`
		Value struct {
			Address string `json:"address"`
		} `json:"value"`
	} `json:"connect_addresses"`
}

// List returns the kubecraft tunnels of the account.
func (c *Client) List(ctx context.Context) ([]Tunnel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api+"/v1/tunnels/list", bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Agent-Key "+c.key)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("playit: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("playit: %w", err)
	}
	var out struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("playit: HTTP %d, not JSON", res.StatusCode)
	}
	if out.Status != "success" {
		return nil, fmt.Errorf("playit: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(out.Data)))
	}
	var data struct {
		Tunnels []rawTunnel `json:"tunnels"`
	}
	if err := json.Unmarshal(out.Data, &data); err != nil {
		return nil, fmt.Errorf("playit: %w", err)
	}
	var tunnels []Tunnel
	for _, r := range data.Tunnels {
		if t, ok := parse(r); ok {
			tunnels = append(tunnels, t)
		}
	}
	return tunnels, nil
}

func parse(r rawTunnel) (Tunnel, bool) {
	if !strings.HasPrefix(r.Name, Prefix) {
		return Tunnel{}, false
	}
	t := Tunnel{ID: r.ID, Name: r.Name, Slug: strings.TrimPrefix(r.Name, Prefix)}
	for _, f := range r.Origin.Details.ConfigData.Fields {
		switch f.Name {
		case "local_ip":
			t.LocalIP = f.Value
		case "local_port":
			t.LocalPort, _ = strconv.Atoi(f.Value)
		}
	}
	t.Address = pickAddress(r)
	t.Problems = append(t.Problems, r.OfflineReason...)
	if !r.UserEnabled {
		t.Problems = append(t.Problems, "tunnel disabled in the playit dashboard")
	}
	if r.TunnelType != "" && r.TunnelType != "minecraft-java" {
		t.Problems = append(t.Problems, "tunnel type must be Minecraft Java, is "+r.TunnelType)
	}
	if r.Origin.Type != "agent" {
		t.Problems = append(t.Problems, "tunnel is not assigned to the agent")
	}
	t.Routable = true
	if t.LocalIP != LocalIP {
		t.Problems = append(t.Problems, "local address must be "+LocalIP+", is "+t.LocalIP)
		t.Routable = false
	}
	if t.LocalPort < 1024 || t.LocalPort > 65535 {
		t.Problems = append(t.Problems, "local port must be between 1024 and 65535")
		t.Routable = false
	}
	if t.Address == "" {
		t.Problems = append(t.Problems, "no public address assigned yet")
	}
	t.Online = len(t.Problems) == 0
	return t, true
}

// pickAddress prefers the name without a port (playit adds an SRV record for it).
func pickAddress(r rawTunnel) string {
	var withPort string
	for _, a := range r.ConnectAddresses {
		addr := a.Value.Address
		if addr == "" {
			continue
		}
		if a.Type == "auto" && !strings.Contains(addr, ":") {
			return addr
		}
		if withPort == "" && (a.Type == "auto" || a.Type == "domain" || a.Type == "addr4") {
			withPort = addr
		}
	}
	return withPort
}

// Watcher keeps the latest tunnels by slug.
type Watcher struct {
	client *Client
	onSync func()

	mu      sync.RWMutex
	bySlug  map[string]Tunnel
	lastErr error
	synced  bool
}

// NewWatcher polls the playit API; onSync runs after each successful list.
func NewWatcher(client *Client, onSync func()) *Watcher {
	return &Watcher{client: client, onSync: onSync, bySlug: map[string]Tunnel{}}
}

// Tunnel returns the tunnel of a server, if one exists.
func (w *Watcher) Tunnel(slug string) (Tunnel, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	t, ok := w.bySlug[slug]
	return t, ok
}

// UsedPorts returns the local ports taken by kubecraft tunnels.
func (w *Watcher) UsedPorts() map[int]bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := map[int]bool{}
	for _, t := range w.bySlug {
		out[t.LocalPort] = true
	}
	return out
}

// LastError is the error of the last list, nil when it worked.
func (w *Watcher) LastError() error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastErr
}

// Run lists tunnels every interval until ctx ends.
func (w *Watcher) Run(ctx context.Context, every time.Duration) {
	for {
		w.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Once lists tunnels once and marks port clashes between them.
func (w *Watcher) Once(ctx context.Context) {
	tunnels, err := w.client.List(ctx)
	if err != nil {
		metrics.PlayitSyncs.WithLabelValues("error").Inc()
		slog.Warn("playit: listing tunnels failed, keeping the last state", "err", err)
		w.mu.Lock()
		w.lastErr = err
		w.mu.Unlock()
		return
	}
	metrics.PlayitSyncs.WithLabelValues("ok").Inc()
	next := Index(tunnels)
	w.mu.Lock()
	w.bySlug, w.lastErr, w.synced = next, nil, true
	w.mu.Unlock()
	if w.onSync != nil {
		w.onSync()
	}
}

// Index groups tunnels by slug. Two tunnels on one local port, or two for one
// server, are flagged so neither is routed by mistake.
func Index(tunnels []Tunnel) map[string]Tunnel {
	sort.Slice(tunnels, func(i, j int) bool { return tunnels[i].Name < tunnels[j].Name })
	byPort := map[int][]string{}
	bySlug := map[string]Tunnel{}
	for _, t := range tunnels {
		if prev, dup := bySlug[t.Slug]; dup {
			prev.Problems = append(prev.Problems, "more than one tunnel named "+t.Name)
			prev.Online, prev.Routable = false, false
			bySlug[t.Slug] = prev
			continue
		}
		bySlug[t.Slug] = t
		byPort[t.LocalPort] = append(byPort[t.LocalPort], t.Slug)
	}
	for port, slugs := range byPort {
		if len(slugs) < 2 {
			continue
		}
		for _, s := range slugs {
			t := bySlug[s]
			t.Problems = append(t.Problems, fmt.Sprintf("local port %d is used by more than one tunnel", port))
			t.Online, t.Routable = false, false
			bySlug[s] = t
		}
	}
	return bySlug
}

// SuggestPort returns the first port from first up that no tunnel uses.
func SuggestPort(used map[int]bool, first int) int {
	for p := first; p <= 65535; p++ {
		if !used[p] {
			return p
		}
	}
	return first
}
