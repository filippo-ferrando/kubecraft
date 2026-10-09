package playit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func fakeAPI(t *testing.T, body string, status int) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tunnels/list" || r.Header.Get("Authorization") != "Agent-Key key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":"error","data":{"type":"auth","message":"InvalidAgentKey"}}`))
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestListRealResponse(t *testing.T) {
	b, err := os.ReadFile("testdata/list.json")
	if err != nil {
		t.Fatal(err)
	}
	tunnels, err := NewClient(fakeAPI(t, string(b), 200), " key\n").List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tunnels) != 2 {
		t.Fatalf("only kubecraft- tunnels count, got %d", len(tunnels))
	}
	s := tunnels[0]
	if s.Slug != "survival" || s.Address != "fried-dome.tun.ply.gg" || s.LocalPort != 25565 || s.LocalIP != "127.0.0.1" || !s.Online ||
		s.AgentID != "76e51581-9f55-42ca-b1c0-ddbcd52982a5" {
		t.Fatalf("survival: %+v", s)
	}
	c := tunnels[1]
	if c.Online || c.Routable || c.Address != "147.185.221.9:40000" || !s.Routable {
		t.Fatalf("creative: %+v", c)
	}
	joined := strings.Join(c.Problems, "; ")
	for _, want := range []string{"AgentOverLimit", "disabled", "local address must be 127.0.0.1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing problem %q in %q", want, joined)
		}
	}
}

func TestListErrors(t *testing.T) {
	if _, err := NewClient(fakeAPI(t, "", 200), "wrong").List(context.Background()); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("bad key: %v", err)
	}
	if _, err := NewClient(fakeAPI(t, "", 429), "key").List(context.Background()); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("rate limit: %v", err)
	}
}

func TestIndexConflicts(t *testing.T) {
	m := Index([]Tunnel{
		{Name: "kubecraft-a", Slug: "a", LocalPort: 30001, Online: true},
		{Name: "kubecraft-b", Slug: "b", LocalPort: 30001, Online: true},
		{Name: "kubecraft-c", Slug: "c", LocalPort: 30003, Online: true, Routable: true},
	})
	if m["a"].Online || m["b"].Online || !m["c"].Online || m["a"].Routable {
		t.Fatalf("shared port must take both offline: %+v", m)
	}
}

func TestSuggestPort(t *testing.T) {
	if p := SuggestPort(map[int]bool{30001: true, 30002: true}, 30001); p != 30003 {
		t.Fatalf("got %d", p)
	}
}

func TestPlan(t *testing.T) {
	tn := func(id, slug string, port int, ok bool) Tunnel {
		return Tunnel{ID: id, Name: Prefix + slug, Slug: slug, LocalIP: LocalIP, LocalPort: port, Routable: ok}
	}
	tunnels := []Tunnel{
		tn("1", "keep", 30001, true),
		tn("2", "gone", 30002, true),   // server no longer public
		tn("3", "dup", 30003, true),    // first of two: kept
		tn("4", "dup", 30004, true),    // second of two: deleted
		tn("5", "broken", 25, false),   // not routable: replaced
		tn("6", "zclash", 30001, true), // same port as keep, sorts later: replaced
	}
	wanted := map[string]bool{"keep": true, "dup": true, "broken": true, "zclash": true, "new": true}
	del, add := Plan(tunnels, wanted, 30001)
	var ids []string
	for _, d := range del {
		ids = append(ids, d.ID)
	}
	if got := strings.Join(ids, ","); got != "5,4,2,6" {
		t.Fatalf("deleted %s", got)
	}
	want := []NewTunnel{{"broken", 30002}, {"new", 30004}, {"zclash", 30005}}
	if fmt.Sprint(add) != fmt.Sprint(want) {
		t.Fatalf("created %v, want %v", add, want)
	}
	if del, add := Plan(nil, map[string]bool{}, 30001); len(del)+len(add) != 0 {
		t.Fatal("nothing to do")
	}
}

// fakePlayit is an in-memory playit API with create and delete.
type fakePlayit struct {
	mu      sync.Mutex
	tunnels []rawTunnel
	next    int
	failOn  string // tunnel name whose create fails
	bodies  []map[string]any
	down    bool
}

func (f *fakePlayit) serve(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		reply := func(status string, data any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "data": data})
		}
		if f.down {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/v1/agents/rundata":
			reply("success", map[string]any{"agent_id": "agent-1"})
		case "/v1/tunnels/list":
			reply("success", map[string]any{"tunnels": f.tunnels})
		case "/v1/tunnels/create":
			f.bodies = append(f.bodies, body)
			name, _ := body["name"].(string)
			if name == f.failOn {
				w.WriteHeader(http.StatusBadRequest)
				reply("fail", "RequiresPlayitPremium")
				return
			}
			f.next++
			origin := body["origin"].(map[string]any)["data"].(map[string]any)
			var rt rawTunnel
			rt.ID, rt.Name, rt.UserEnabled, rt.TunnelType = fmt.Sprintf("t%d", f.next), name, true, "minecraft-java"
			rt.Origin.Type = "agent"
			rt.Origin.Details.AgentID = origin["agent_id"].(string)
			for _, x := range origin["config"].(map[string]any)["fields"].([]any) {
				m := x.(map[string]any)
				rt.Origin.Details.ConfigData.Fields = append(rt.Origin.Details.ConfigData.Fields, field{m["name"].(string), m["value"].(string)})
			}
			rt.ConnectAddresses = append(rt.ConnectAddresses, struct {
				Type  string `json:"type"`
				Value struct {
					Address string `json:"address"`
				} `json:"value"`
			}{Type: "auto"})
			rt.ConnectAddresses[0].Value.Address = name + ".tun.ply.gg"
			f.tunnels = append(f.tunnels, rt)
			reply("success", map[string]any{"id": rt.ID})
		case "/tunnels/delete":
			for i, rt := range f.tunnels {
				if rt.ID == body["tunnel_id"] {
					f.tunnels = append(f.tunnels[:i], f.tunnels[i+1:]...)
					reply("success", nil)
					return
				}
			}
			reply("fail", "TunnelNotFound")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *fakePlayit) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, t := range f.tunnels {
		out = append(out, t.Name)
	}
	return out
}

func TestWatcherManagesTunnels(t *testing.T) {
	ctx := context.Background()
	f := &fakePlayit{failOn: "kubecraft-big"}
	// A tunnel of another agent with a kubecraft name is never touched.
	var foreign rawTunnel
	foreign.ID, foreign.Name, foreign.Origin.Type, foreign.Origin.Details.AgentID = "x", "kubecraft-other", "agent", "agent-2"
	f.tunnels = append(f.tunnels, foreign)
	synced := 0
	w := NewWatcher(NewClient(f.serve(t), "key"), 30001, func() { synced++ })

	// Before the servers are known nothing changes.
	w.Once(ctx)
	if got := f.names(); len(got) != 1 || synced != 1 {
		t.Fatalf("changed before Want: %v", got)
	}

	w.Want(map[string]bool{"survival": true, "creative": true, "big": true})
	w.Once(ctx)
	if got := strings.Join(f.names(), ","); got != "kubecraft-other,kubecraft-creative,kubecraft-survival" {
		t.Fatalf("tunnels: %s", got)
	}
	s, ok := w.Tunnel("survival")
	if !ok || s.LocalPort != 30003 || s.Address != "kubecraft-survival.tun.ply.gg" || !s.Routable || !s.Online {
		t.Fatalf("survival: %+v", s)
	}
	if c, _ := w.Tunnel("creative"); c.LocalPort != 30002 {
		t.Fatalf("creative: %+v", c)
	}
	if _, ok := w.Tunnel("other"); ok {
		t.Fatal("tunnel of another agent indexed")
	}
	if msg := w.Failure("big"); !strings.Contains(msg, "no free tunnels") {
		t.Fatalf("failure: %q", msg)
	}

	// The create request has the shape the playit API accepts.
	b := f.bodies[0]
	if b["protocol"].(map[string]any)["details"] != "minecraft-java" ||
		b["origin"].(map[string]any)["data"].(map[string]any)["agent_id"] != "agent-1" ||
		b["endpoint"].(map[string]any)["details"].(map[string]any)["region"] != "global" {
		t.Fatalf("create body: %v", b)
	}

	// A server turned private loses its tunnel; its failure is forgotten.
	w.Want(map[string]bool{"survival": true})
	w.Once(ctx)
	if got := strings.Join(f.names(), ","); got != "kubecraft-other,kubecraft-survival" {
		t.Fatalf("after private: %s", got)
	}
	if _, ok := w.Tunnel("creative"); ok || w.Failure("big") != "" {
		t.Fatal("stale state")
	}

	// A failing API keeps the last good state.
	f.mu.Lock()
	f.down = true
	f.mu.Unlock()
	w.Once(ctx)
	if _, ok := w.Tunnel("survival"); !ok || w.LastError() == nil {
		t.Fatal("state lost on error")
	}
}

func TestWantKicks(t *testing.T) {
	w := NewWatcher(nil, 30001, nil)
	w.Want(map[string]bool{"a": true})
	select {
	case <-w.kick:
	default:
		t.Fatal("first Want must kick")
	}
	w.Want(map[string]bool{"a": true})
	select {
	case <-w.kick:
		t.Fatal("same set must not kick")
	default:
	}
}
