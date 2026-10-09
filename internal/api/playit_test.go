package api

import (
	"context"
	"net/http"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/k8s"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/playit"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/reconcile"
)

// fakeTunnels stands in for the playit watcher.
type fakeTunnels struct {
	mu sync.Mutex
	m  map[string]playit.Tunnel
}

func (f *fakeTunnels) set(slug string, t playit.Tunnel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.Name == "" {
		delete(f.m, slug)
		return
	}
	f.m[slug] = t
}

func (f *fakeTunnels) Tunnel(slug string) (playit.Tunnel, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.m[slug]
	return t, ok
}

func (f *fakeTunnels) UsedPorts() map[int]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int]bool{}
	for _, t := range f.m {
		out[t.LocalPort] = true
	}
	return out
}

func (f *fakeTunnels) LastError() error { return nil }

func TestPlayitTunnels(t *testing.T) {
	tunnels := &fakeTunnels{m: map[string]playit.Tunnel{
		"other": {Name: "kubecraft-other", Slug: "other", LocalPort: 30001, Routable: true, Online: true},
	}}
	e := newTestEnv(t, func(d *Deps) {
		d.Config.PlayitSecret = "key"
		d.Config.PlayitFirstPort = 30001
		d.Mgr.SetTunnels(tunnels)
	})
	e.createUser("alice", auth.RoleUser, false)
	a := e.login("alice")
	ctx := context.Background()

	var meta struct {
		Playit struct {
			Enabled bool `json:"enabled"`
		} `json:"playit"`
	}
	a.do("GET", "/api/meta", nil).expect(http.StatusOK).decode(&meta)
	if !meta.Playit.Enabled {
		t.Fatal("playit not reported")
	}

	// Before the tunnel exists: private address and instructions.
	var srv serverJSON
	a.do("POST", "/api/servers", createReq("survival", e.plan("Small"))).expect(http.StatusCreated).decode(&srv)
	if srv.Address != "survival.mine.test" || srv.Playit == nil || srv.Playit.Found ||
		srv.Playit.TunnelName != "kubecraft-survival" || srv.Playit.SuggestedPort != 30002 || srv.Playit.LocalIP != "127.0.0.1" {
		t.Fatalf("no tunnel yet: %+v %+v", srv, srv.Playit)
	}

	// The tunnel shows up: the playit address becomes the address and the gateway gets the port.
	tunnels.set("survival", playit.Tunnel{Name: "kubecraft-survival", Slug: "survival", Address: "fried-dome.tun.ply.gg",
		LocalIP: "127.0.0.1", LocalPort: 30002, Routable: true, Online: true})
	rec := reconcile.New(e.store, e.deps.Mgr)
	if err := rec.Once(ctx); err != nil {
		t.Fatal(err)
	}
	base := "/api/servers/" + srv.ID.String()
	srv = serverJSON{}
	a.do("GET", base, nil).expect(http.StatusOK).decode(&srv)
	if srv.Address != "fried-dome.tun.ply.gg" || srv.PrivateAddress != "survival.mine.test" || !srv.Playit.Online || srv.Playit.LocalPort != 30002 {
		t.Fatalf("with tunnel: %+v %+v", srv, srv.Playit)
	}
	svc, _ := e.kube.CoreV1().Services("minecraft").Get(ctx, "mc-survival", metav1.GetOptions{})
	if svc.Labels[k8s.LabelPlayit] != "true" || svc.Annotations[k8s.AnnotationPlayitPort] != "30002" {
		t.Fatalf("service: %v %v", svc.Labels, svc.Annotations)
	}

	// A misconfigured tunnel is shown but never routed.
	tunnels.set("survival", playit.Tunnel{Name: "kubecraft-survival", Slug: "survival", Address: "fried-dome.tun.ply.gg",
		LocalIP: "192.168.1.9", LocalPort: 30002, Problems: []string{"local address must be 127.0.0.1"}})
	if err := rec.Once(ctx); err != nil {
		t.Fatal(err)
	}
	svc, _ = e.kube.CoreV1().Services("minecraft").Get(ctx, "mc-survival", metav1.GetOptions{})
	if svc.Labels[k8s.LabelPlayit] != "false" || svc.Annotations[k8s.AnnotationPlayitPort] != "" {
		t.Fatalf("bad tunnel routed: %v %v", svc.Labels, svc.Annotations)
	}
	srv = serverJSON{}
	a.do("GET", base, nil).expect(http.StatusOK).decode(&srv)
	if srv.Playit.Online || len(srv.Playit.Problems) == 0 {
		t.Fatalf("problems not shown: %+v", srv.Playit)
	}

	// Tunnel deleted: back to the private address, port removed.
	tunnels.set("survival", playit.Tunnel{})
	if err := rec.Once(ctx); err != nil {
		t.Fatal(err)
	}
	srv = serverJSON{}
	a.do("GET", base, nil).expect(http.StatusOK).decode(&srv)
	if srv.Address != "survival.mine.test" || srv.Playit.Found {
		t.Fatalf("after delete: %+v", srv)
	}
}

func TestNoPlayit(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", auth.RoleUser, false)
	a := e.login("alice")
	var srv serverJSON
	a.do("POST", "/api/servers", createReq("plain", e.plan("Small"))).expect(http.StatusCreated).decode(&srv)
	if srv.Address != "plain.mine.test" || srv.Playit != nil {
		t.Fatalf("without playit: %+v", srv)
	}
}
