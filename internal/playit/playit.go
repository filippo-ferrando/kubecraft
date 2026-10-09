// Package playit manages the playit.gg tunnels of public servers. The agent key must
// belong to a self-managed agent, which may create and delete its own tunnels; each
// public server gets a tunnel named kubecraft-<slug> on a free local port.
package playit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
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
	// Prefix of tunnel names kubecraft manages: kubecraft-<slug>.
	Prefix = "kubecraft-"
	// LocalIP is where every kubecraft tunnel points; the gateway shares the agent's pod.
	LocalIP = "127.0.0.1"
)

// Tunnel is what kubecraft needs to know about one tunnel.
type Tunnel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"-"`
	AgentID   string `json:"-"`
	Address   string `json:"address"`
	LocalIP   string `json:"local_ip"`
	LocalPort int    `json:"local_port"`
	Online    bool   `json:"online"`
	// Routable means the gateway can serve it: right local address, valid and unshared port.
	Routable bool     `json:"routable"`
	Problems []string `json:"problems"`
}

// APIError is an answer of the playit API that is not a success.
type APIError struct {
	HTTP int
	// Code is the playit error name, for example RequiresPlayitPremium.
	Code string
}

func (e *APIError) Error() string {
	if msg, ok := errorText[e.Code]; ok {
		return "playit: " + msg
	}
	return fmt.Sprintf("playit: HTTP %d: %s", e.HTTP, e.Code)
}

var errorText = map[string]string{
	"NotAllowedWithReadOnly":  "the agent key is read only, use a key of a self-managed agent (see docs/components/playit.md)",
	"AgentNotSelfManaged":     "the agent key is not of a self-managed agent (see docs/components/playit.md)",
	"InvalidAgentKey":         "the agent key is not valid",
	"RequiresPlayitPremium":   "the playit.gg account has no free tunnels left",
	"RequiresVerifiedAccount": "the playit.gg account must be verified before tunnels can be created",
	"AgentVersionTooOld":      "the playit agent has not connected yet, or is too old",
	"AgentNotFound":           "the playit agent was not found",
}

// Client talks to the playit API with the agent key.
type Client struct {
	api  string
	key  string
	http *http.Client
}

func NewClient(api, key string) *Client {
	return &Client{api: strings.TrimSuffix(api, "/"), key: strings.TrimSpace(key), http: &http.Client{Timeout: 15 * time.Second}}
}

// call posts body to path and decodes the data of a success answer into out.
func (c *Client) call(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Agent-Key "+c.key)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("playit: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("playit: %w", err)
	}
	var env struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("playit: HTTP %d, not JSON", res.StatusCode)
	}
	if env.Status != "success" {
		return &APIError{HTTP: res.StatusCode, Code: errorCode(env.Data)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("playit: %w", err)
	}
	return nil
}

// errorCode reads a fail answer ("Code") or an error answer ({"message": "Code"}).
func errorCode(data json.RawMessage) string {
	var s string
	if json.Unmarshal(data, &s) == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	return strings.TrimSpace(string(data))
}

// AgentID returns the id of the agent the key belongs to.
func (c *Client) AgentID(ctx context.Context) (string, error) {
	var out struct {
		AgentID string `json:"agent_id"`
	}
	if err := c.call(ctx, "/v1/agents/rundata", struct{}{}, &out); err != nil {
		return "", err
	}
	if out.AgentID == "" {
		return "", errors.New("playit: no agent id in rundata")
	}
	return out.AgentID, nil
}

type field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Create adds a Minecraft Java tunnel on the agent pointing to 127.0.0.1:port.
func (c *Client) Create(ctx context.Context, agentID, name string, port int) (string, error) {
	fields := []field{{"local_ip", LocalIP}, {"local_port", strconv.Itoa(port)}}
	body := map[string]any{
		"name":     name,
		"protocol": map[string]any{"type": "tunnel-type", "details": "minecraft-java"},
		"origin": map[string]any{"type": "agent", "data": map[string]any{
			"agent_id": agentID, "config": map[string]any{"fields": fields},
		}},
		"endpoint": map[string]any{"type": "region", "details": map[string]any{"region": "global", "port": nil}},
		"enabled":  true,
	}
	var out struct {
		ID string `json:"id"`
	}
	err := c.call(ctx, "/v1/tunnels/create", body, &out)
	return out.ID, err
}

// Delete removes a tunnel.
func (c *Client) Delete(ctx context.Context, id string) error {
	return c.call(ctx, "/tunnels/delete", map[string]string{"tunnel_id": id}, nil)
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
			AgentID    string `json:"agent_id"`
			ConfigData struct {
				Fields []field `json:"fields"`
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
	var data struct {
		Tunnels []rawTunnel `json:"tunnels"`
	}
	if err := c.call(ctx, "/v1/tunnels/list", struct{}{}, &data); err != nil {
		return nil, err
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
	t := Tunnel{ID: r.ID, Name: r.Name, Slug: strings.TrimPrefix(r.Name, Prefix), AgentID: r.Origin.Details.AgentID}
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
	// A missing address is normal for a few seconds after create, so not a problem.
	t.Online = len(t.Problems) == 0 && t.Address != ""
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

// API is the part of the client the watcher uses.
type API interface {
	AgentID(ctx context.Context) (string, error)
	List(ctx context.Context) ([]Tunnel, error)
	Create(ctx context.Context, agentID, name string, port int) (string, error)
	Delete(ctx context.Context, id string) error
}

// Watcher keeps one tunnel per public server and the latest tunnels by slug.
type Watcher struct {
	api       API
	firstPort int
	onSync    func()
	kick      chan struct{}

	mu      sync.RWMutex
	bySlug  map[string]Tunnel
	lastErr error
	agentID string
	// wanted is nil until the first Want, so nothing is deleted before the servers are known.
	wanted map[string]bool
	// failures holds the last create error per slug.
	failures map[string]string
}

// NewWatcher syncs tunnels with the API; onSync runs after each successful sync.
func NewWatcher(api API, firstPort int, onSync func()) *Watcher {
	return &Watcher{api: api, firstPort: firstPort, onSync: onSync, kick: make(chan struct{}, 1),
		bySlug: map[string]Tunnel{}, failures: map[string]string{}}
}

// Want sets the servers that must have a tunnel and syncs soon when it changed.
func (w *Watcher) Want(slugs map[string]bool) {
	w.mu.Lock()
	changed := w.wanted == nil || !maps.Equal(w.wanted, slugs)
	w.wanted = maps.Clone(slugs)
	w.mu.Unlock()
	if changed {
		w.Kick()
	}
}

// Kick asks for a sync without waiting for the interval.
func (w *Watcher) Kick() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// Tunnel returns the tunnel of a server, if one exists.
func (w *Watcher) Tunnel(slug string) (Tunnel, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	t, ok := w.bySlug[slug]
	return t, ok
}

// Failure is the last error creating the tunnel of a server, empty if none.
func (w *Watcher) Failure(slug string) string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.failures[slug]
}

// LastError is the error of the last sync, nil when it worked.
func (w *Watcher) LastError() error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastErr
}

// Run syncs every interval, sooner when kicked or while an address is pending.
func (w *Watcher) Run(ctx context.Context, every time.Duration) {
	for {
		w.Once(ctx)
		wait := every
		if w.pending() {
			wait = min(every, 5*time.Second)
		}
		select {
		case <-ctx.Done():
			return
		case <-w.kick:
		case <-time.After(wait):
		}
	}
}

// pending reports whether a wanted tunnel exists but has no address yet.
func (w *Watcher) pending() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for slug := range w.wanted {
		if t, ok := w.bySlug[slug]; ok && t.Address == "" {
			return true
		}
	}
	return false
}

// Once syncs tunnels once; on error the last state is kept.
func (w *Watcher) Once(ctx context.Context) {
	tunnels, err := w.sync(ctx)
	if err != nil {
		metrics.PlayitSyncs.WithLabelValues("error").Inc()
		slog.Warn("playit: sync failed, keeping the last state", "err", err)
		w.mu.Lock()
		w.lastErr = err
		w.mu.Unlock()
		return
	}
	metrics.PlayitSyncs.WithLabelValues("ok").Inc()
	next := Index(tunnels)
	w.mu.Lock()
	w.bySlug, w.lastErr = next, nil
	w.mu.Unlock()
	if w.onSync != nil {
		w.onSync()
	}
}

func (w *Watcher) sync(ctx context.Context) ([]Tunnel, error) {
	w.mu.RLock()
	agentID, wanted := w.agentID, maps.Clone(w.wanted)
	w.mu.RUnlock()
	if agentID == "" {
		id, err := w.api.AgentID(ctx)
		if err != nil {
			return nil, err
		}
		agentID = id
		w.mu.Lock()
		w.agentID = id
		w.mu.Unlock()
	}
	tunnels, err := w.list(ctx, agentID)
	if err != nil || wanted == nil {
		return tunnels, err
	}
	del, add := Plan(tunnels, wanted, w.firstPort)
	if len(del) == 0 && len(add) == 0 {
		return tunnels, nil
	}
	for _, t := range del {
		err := w.api.Delete(ctx, t.ID)
		metrics.PlayitTunnelActions.WithLabelValues("delete", result(err)).Inc()
		if err != nil {
			slog.Warn("playit: delete tunnel failed", "tunnel", t.Name, "err", err)
		} else {
			slog.Info("playit: tunnel deleted", "tunnel", t.Name)
		}
	}
	for _, c := range add {
		_, err := w.api.Create(ctx, agentID, Prefix+c.Slug, c.Port)
		metrics.PlayitTunnelActions.WithLabelValues("create", result(err)).Inc()
		w.mu.Lock()
		if err != nil {
			w.failures[c.Slug] = err.Error()
		} else {
			delete(w.failures, c.Slug)
		}
		w.mu.Unlock()
		if err != nil {
			slog.Warn("playit: create tunnel failed", "server", c.Slug, "err", err)
		} else {
			slog.Info("playit: tunnel created", "server", c.Slug, "port", c.Port)
		}
	}
	w.mu.Lock()
	for slug := range w.failures {
		if !wanted[slug] {
			delete(w.failures, slug)
		}
	}
	w.mu.Unlock()
	return w.list(ctx, agentID)
}

// list returns the kubecraft tunnels of this agent; tunnels of other agents are ignored.
func (w *Watcher) list(ctx context.Context, agentID string) ([]Tunnel, error) {
	all, err := w.api.List(ctx)
	if err != nil {
		return nil, err
	}
	own := all[:0]
	for _, t := range all {
		if t.AgentID == "" || t.AgentID == agentID {
			own = append(own, t)
		}
	}
	return own, nil
}

func result(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

// NewTunnel is a tunnel Plan wants created.
type NewTunnel struct {
	Slug string
	Port int
}

// Plan compares tunnels with the wanted servers. Tunnels of unwanted servers,
// extra tunnels of one server, and tunnels that cannot be routed are deleted;
// wanted servers without a usable tunnel get one on a free port.
func Plan(tunnels []Tunnel, wanted map[string]bool, firstPort int) (del []Tunnel, add []NewTunnel) {
	sorted := sortTunnels(tunnels)
	used := map[int]bool{}
	have := map[string]bool{}
	for _, t := range sorted {
		if !wanted[t.Slug] || have[t.Slug] || !t.Routable || used[t.LocalPort] {
			del = append(del, t)
			continue
		}
		have[t.Slug] = true
		used[t.LocalPort] = true
	}
	slugs := make([]string, 0, len(wanted))
	for s, ok := range wanted {
		if ok && !have[s] {
			slugs = append(slugs, s)
		}
	}
	sort.Strings(slugs)
	for _, s := range slugs {
		p := SuggestPort(used, firstPort)
		used[p] = true
		add = append(add, NewTunnel{Slug: s, Port: p})
	}
	return del, add
}

// sortTunnels returns a copy sorted by name, then id, so plans are stable.
func sortTunnels(in []Tunnel) []Tunnel {
	out := append([]Tunnel(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Index groups tunnels by slug. Two tunnels on one local port, or two for one
// server, are flagged so neither is routed by mistake.
func Index(tunnels []Tunnel) map[string]Tunnel {
	tunnels = sortTunnels(tunnels)
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

// SuggestPort returns the first port from first up that is not used.
func SuggestPort(used map[int]bool, first int) int {
	for p := first; p <= 65535; p++ {
		if !used[p] {
			return p
		}
	}
	return first
}
