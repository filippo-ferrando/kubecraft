package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
)

// runningPod adds a started pod for the server so logs can be streamed.
func (e *testEnv) runningPod(srv serverJSON) {
	e.t.Helper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "mc-" + srv.Slug + "-0", Namespace: "minecraft", Labels: map[string]string{
			"app.kubernetes.io/managed-by": "mc-manager", "mc-manager/server-id": srv.ID.String(),
		}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
			Name: "minecraft", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}}},
	}
	if _, err := e.kube.CoreV1().Pods("minecraft").Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		e.t.Fatal(err)
	}
}

func (c *client) ws(path string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", c.origin)
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(c.base, "http")+path, &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: h})
}

// readUntil reads messages until one matches or the timeout passes.
func readUntil(t *testing.T, conn *websocket.Conn, match func(termMsg) bool) termMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var m termMsg
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatalf("read: %v", err)
		}
		if match(m) {
			return m
		}
	}
}

func TestLogsAndConsole(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", auth.RoleUser, false)
	a := e.login("alice")
	var srv serverJSON
	a.do("POST", "/api/servers", createReq("term", e.plan("Small"))).expect(http.StatusCreated).decode(&srv)
	e.runningPod(srv)
	base := "/api/servers/" + srv.ID.String()

	// The fake clientset returns "fake logs" as the pod log.
	logs, _, err := a.ws(base + "/logs")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logs.CloseNow() }()
	readUntil(t, logs, func(m termMsg) bool { return m.Type == "log" && m.Line == "fake logs" })

	con, _, err := a.ws(base + "/console")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = con.CloseNow() }()
	if err := wsjson.Write(context.Background(), con, termMsg{Type: "command", Command: "/say hello"}); err != nil {
		t.Fatal(err)
	}
	out := readUntil(t, con, func(m termMsg) bool { return m.Type == "output" })
	if out.Text != "ok: say hello" {
		t.Fatalf("output: %+v", out)
	}

	// Every command is audited.
	entries, err := e.store.ListAudit(context.Background(), sqlcAuditParams(50))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, en := range entries {
		if en.Action == "console.command" && strings.Contains(string(en.Details), "say hello") {
			found = true
		}
	}
	if !found {
		t.Fatal("console command not audited")
	}
}

func TestConsoleAccess(t *testing.T) {
	e := newTestEnv(t)
	alice := e.createUser("alice", auth.RoleUser, false)
	e.createUser("bob", auth.RoleUser, false)
	a := e.login("alice")
	var srv serverJSON
	a.do("POST", "/api/servers", createReq("acl", e.plan("Small"))).expect(http.StatusCreated).decode(&srv)
	base := "/api/servers/" + srv.ID.String()

	// Other users get 404 on the socket.
	if _, res, err := e.login("bob").ws(base + "/console"); err == nil || res == nil || res.StatusCode != http.StatusNotFound {
		t.Fatalf("bob: expected 404, got %v", res)
	}
	// Cross-origin sockets are rejected.
	evil := e.login("alice")
	evil.origin = "https://evil.example"
	if _, res, err := evil.ws(base + "/console"); err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross origin: expected 403, got %v", res)
	}
	// A disabled console blocks commands but logs still work.
	if _, err := e.store.UpdateUserFlags(context.Background(), sqlcFlags(alice.ID, "user", false, false)); err != nil {
		t.Fatal(err)
	}
	if _, res, err := a.ws(base + "/console"); err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("disabled console: expected 403, got %v", res)
	}
	conn, _, err := a.ws(base + "/logs")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.CloseNow()
}

func TestPlayerActions(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", auth.RoleUser, false)
	a := e.login("alice")
	var srv serverJSON
	a.do("POST", "/api/servers", createReq("players", e.plan("Small"))).expect(http.StatusCreated).decode(&srv)
	base := "/api/servers/" + srv.ID.String()

	var res struct {
		AppliedLive bool       `json:"applied_live"`
		Server      serverJSON `json:"server"`
	}
	a.do("POST", base+"/players", map[string]string{"action": "whitelist_add", "name": "Steve"}).expect(http.StatusOK).decode(&res)
	if !res.AppliedLive || len(res.Server.Settings.Whitelist) != 1 || res.Server.Settings.Whitelist[0] != "Steve" {
		t.Fatalf("whitelist add: %+v", res)
	}
	select {
	case cmd := <-e.rconCmds:
		if cmd != "whitelist add Steve" {
			t.Fatalf("rcon got %q", cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no rcon command")
	}
	a.do("POST", base+"/players", map[string]string{"action": "op", "name": "Steve"}).expect(http.StatusOK).decode(&res)
	a.do("POST", base+"/players", map[string]string{"action": "whitelist_remove", "name": "steve"}).expect(http.StatusOK).decode(&res)
	if len(res.Server.Settings.Whitelist) != 0 || len(res.Server.Settings.Ops) != 1 {
		t.Fatalf("after remove: %+v", res.Server.Settings)
	}
	a.do("POST", base+"/players", map[string]string{"action": "op", "name": "bad name!"}).expect(http.StatusBadRequest)
	a.do("POST", base+"/players", map[string]string{"action": "ban", "name": "Steve"}).expect(http.StatusBadRequest)

	// Player list changes do not need a restart.
	if res.Server.Status.RestartRequired {
		t.Fatal("restart required after a whitelist change")
	}

	a.do("POST", base+"/stop", nil).expect(http.StatusOK)
	a.do("POST", base+"/players", map[string]string{"action": "kick", "name": "Steve"}).expect(http.StatusConflict)
	a.do("POST", base+"/players", map[string]string{"action": "whitelist_add", "name": "Alex"}).expect(http.StatusOK).decode(&res)
	if res.AppliedLive {
		t.Fatal("stopped server cannot apply live")
	}
}

func TestUpdateAppliesWorldSettings(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", auth.RoleUser, false)
	a := e.login("alice")
	var srv serverJSON
	a.do("POST", "/api/servers", createReq("live", e.plan("Small"))).expect(http.StatusCreated).decode(&srv)
	a.do("PATCH", "/api/servers/"+srv.ID.String(), map[string]any{"settings": map[string]any{"difficulty": "peaceful"}}).expect(http.StatusOK)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case cmd := <-e.rconCmds:
			if cmd == "difficulty peaceful" {
				return
			}
		case <-deadline:
			t.Fatal("difficulty not applied over RCON")
		}
	}
}
