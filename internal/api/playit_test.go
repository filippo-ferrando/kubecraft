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
	mu       sync.Mutex
	m        map[string]playit.Tunnel
	failures map[string]string
	wanted   map[string]bool
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

func (f *fakeTunnels) Failure(slug string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failures[slug]
}

func (f *fakeTunnels) Want(slugs map[string]bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wanted = slugs
}

func (f *fakeTunnels) LastError() error { return nil }

func publicReq(slug string, plan planJSON, public bool) map[string]any {
	req := createReq(slug, plan)
	req["settings"].(map[string]any)["public"] = public
	return req
}

func TestPlayitTunnels(t *testing.T) {
	tunnels := &fakeTunnels{m: map[string]playit.Tunnel{}, failures: map[string]string{}}
	e := newTestEnv(t, func(d *Deps) {
		d.Config.PlayitSecret = "key"
		d.Mgr.SetTunnels(tunnels)
	})
	e.createUser("alice", auth.RoleUser, false)
	a := e.login("alice")
	ctx := context.Background()
	rec := reconcile.New(e.store, e.deps.Mgr)

	var meta struct {
		Playit struct {
			Enabled bool `json:"enabled"`
		} `json:"playit"`
	}
	a.do("GET", "/api/meta", nil).expect(http.StatusOK).decode(&meta)
	if !meta.Playit.Enabled {
		t.Fatal("playit not reported")
	}

	// A private server has no playit block and wants no tunnel.
	var priv serverJSON
	a.do("POST", "/api/servers", publicReq("lan", e.plan("Small"), false)).expect(http.StatusCreated).decode(&priv)
	if priv.Playit != nil || priv.Address != "lan.mine.test" {
		t.Fatalf("private: %+v", priv)
	}

	// A public server asks for a tunnel; until it exists the private address is used.
	var srv serverJSON
	a.do("POST", "/api/servers", publicReq("survival", e.plan("Small"), true)).expect(http.StatusCreated).decode(&srv)
	if srv.Address != "survival.mine.test" || srv.Playit == nil || srv.Playit.Found || srv.Playit.TunnelName != "kubecraft-survival" {
		t.Fatalf("no tunnel yet: %+v %+v", srv, srv.Playit)
	}
	if err := rec.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if len(tunnels.wanted) != 1 || !tunnels.wanted["survival"] {
		t.Fatalf("wanted: %v", tunnels.wanted)
	}

	// A failed create is shown on the server.
	tunnels.failures["survival"] = "playit: the playit.gg account has no free tunnels left"
	base := "/api/servers/" + srv.ID.String()
	srv = serverJSON{}
	a.do("GET", base, nil).expect(http.StatusOK).decode(&srv)
	if len(srv.Playit.Problems) != 1 {
		t.Fatalf("failure not shown: %+v", srv.Playit)
	}

	// The tunnel shows up: the playit address becomes the address and the gateway gets the port.
	tunnels.set("survival", playit.Tunnel{Name: "kubecraft-survival", Slug: "survival", Address: "fried-dome.tun.ply.gg",
		LocalIP: "127.0.0.1", LocalPort: 30002, Routable: true, Online: true})
	if err := rec.Once(ctx); err != nil {
		t.Fatal(err)
	}
	srv = serverJSON{}
	a.do("GET", base, nil).expect(http.StatusOK).decode(&srv)
	if srv.Address != "fried-dome.tun.ply.gg" || srv.PrivateAddress != "survival.mine.test" || !srv.Playit.Online || srv.Playit.LocalPort != 30002 {
		t.Fatalf("with tunnel: %+v %+v", srv, srv.Playit)
	}
	svc, _ := e.kube.CoreV1().Services("minecraft").Get(ctx, "mc-survival", metav1.GetOptions{})
	if svc.Labels[k8s.LabelPlayit] != "true" || svc.Annotations[k8s.AnnotationPlayitPort] != "30002" {
		t.Fatalf("service: %v %v", svc.Labels, svc.Annotations)
	}

	// Turned private: no tunnel wanted, the port leaves the Service even before the tunnel is gone.
	a.do("PATCH", base, map[string]any{"settings": map[string]any{"public": false}}).expect(http.StatusOK)
	if err := rec.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if len(tunnels.wanted) != 0 {
		t.Fatalf("still wanted: %v", tunnels.wanted)
	}
	svc, _ = e.kube.CoreV1().Services("minecraft").Get(ctx, "mc-survival", metav1.GetOptions{})
	if svc.Labels[k8s.LabelPlayit] != "false" || svc.Annotations[k8s.AnnotationPlayitPort] != "" {
		t.Fatalf("private server routed: %v %v", svc.Labels, svc.Annotations)
	}
	srv = serverJSON{}
	a.do("GET", base, nil).expect(http.StatusOK).decode(&srv)
	if srv.Address != "survival.mine.test" || srv.Playit != nil {
		t.Fatalf("after private: %+v", srv)
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
	a.do("POST", "/api/servers", publicReq("open", e.plan("Small"), true)).expect(http.StatusBadRequest)
}
