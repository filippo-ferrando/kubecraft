package playit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	if s.Slug != "survival" || s.Address != "fried-dome.tun.ply.gg" || s.LocalPort != 25565 || s.LocalIP != "127.0.0.1" || !s.Online {
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
	if _, err := NewClient(fakeAPI(t, "", 200), "wrong").List(context.Background()); err == nil || !strings.Contains(err.Error(), "InvalidAgentKey") {
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

func TestWatcher(t *testing.T) {
	b, _ := os.ReadFile("testdata/list.json")
	synced := 0
	w := NewWatcher(NewClient(fakeAPI(t, string(b), 200), "key"), func() { synced++ })
	w.Once(context.Background())
	if tn, ok := w.Tunnel("survival"); !ok || tn.Address != "fried-dome.tun.ply.gg" || synced != 1 || w.LastError() != nil {
		t.Fatalf("watcher: %+v %v", tn, w.LastError())
	}
	if !w.UsedPorts()[25565] {
		t.Fatal("used ports")
	}
	// A failing list keeps the last good state.
	w.client = NewClient(fakeAPI(t, "", 500), "key")
	w.Once(context.Background())
	if _, ok := w.Tunnel("survival"); !ok || w.LastError() == nil {
		t.Fatal("state lost on error")
	}
}
