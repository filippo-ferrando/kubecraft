package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/reconcile"
)

func (e *testEnv) plan(name string) planJSON {
	e.t.Helper()
	plans, err := e.store.ListPlans(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, p := range plans {
		if p.Name == name {
			return toPlanJSON(p)
		}
	}
	e.t.Fatalf("plan %s not found", name)
	return planJSON{}
}

func createReq(slug string, plan planJSON) map[string]any {
	return map[string]any{
		"slug":         slug,
		"display_name": "World " + slug,
		"type":         "PAPER",
		"version":      "LATEST",
		"plan_id":      plan.ID,
		"settings":     map[string]any{"difficulty": "hard", "max_players": 10},
		"accept_eula":  true,
	}
}

func TestCreateServerAndQuota(t *testing.T) {
	e := newTestEnv(t)
	alice := e.createUser("alice", auth.RoleUser, false)
	e.createUser("bob", auth.RoleUser, false)
	ctx := context.Background()
	if _, err := e.store.UpsertUserQuota(ctx, sqlc.UpsertUserQuotaParams{UserID: alice.ID, MaxServers: pgtype.Int4{Int32: 2, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	small := e.plan("Small")
	a := e.login("alice")

	var srv serverJSON
	a.do("POST", "/api/servers", createReq("one", small)).expect(http.StatusCreated).decode(&srv)
	if srv.Address != "one.mine.test" || srv.Type != "PAPER" || srv.Settings.Difficulty != "hard" || srv.DesiredState != "running" {
		t.Fatalf("unexpected server: %+v", srv)
	}

	// Pod limits match the plan and the heap is 75% of the memory limit.
	sts, err := e.kube.AppsV1().StatefulSets("minecraft").Get(ctx, "mc-one", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := sts.Spec.Template.Spec.Containers[0]
	if c.Resources.Limits.Memory().Value() != 2048<<20 || c.Resources.Limits.Cpu().MilliValue() != 1000 {
		t.Fatalf("limits do not match plan: %v", c.Resources.Limits)
	}
	for _, env := range c.Env {
		if env.Name == "MEMORY" && env.Value != "1536M" {
			t.Fatalf("heap: %s", env.Value)
		}
	}

	a.do("POST", "/api/servers", createReq("two", small)).expect(http.StatusCreated)
	res := a.do("POST", "/api/servers", createReq("three", small)).expect(http.StatusConflict)
	if res.errorMsg() != "Quota exceeded: 2/2 servers" {
		t.Fatalf("quota message: %q", res.errorMsg())
	}

	var mine []serverJSON
	a.do("GET", "/api/servers", nil).expect(http.StatusOK).decode(&mine)
	if len(mine) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(mine))
	}

	var q struct {
		Usage struct {
			Servers int `json:"servers"`
		} `json:"usage"`
		Limits struct {
			MaxServers *int `json:"max_servers"`
		} `json:"limits"`
	}
	a.do("GET", "/api/me/quota", nil).expect(http.StatusOK).decode(&q)
	if q.Usage.Servers != 2 || q.Limits.MaxServers == nil || *q.Limits.MaxServers != 2 {
		t.Fatalf("quota: %+v", q)
	}

	// Another user cannot see or touch alice's servers.
	b := e.login("bob")
	var bobs []serverJSON
	b.do("GET", "/api/servers", nil).expect(http.StatusOK).decode(&bobs)
	if len(bobs) != 0 {
		t.Fatal("bob sees alice's servers")
	}
	base := "/api/servers/" + srv.ID.String()
	b.do("GET", base, nil).expect(http.StatusNotFound)
	b.do("GET", base+"/status", nil).expect(http.StatusNotFound)
	b.do("PATCH", base, map[string]any{"display_name": "pwned"}).expect(http.StatusNotFound)
	b.do("POST", base+"/stop", nil).expect(http.StatusNotFound)
	b.do("POST", base+"/start", nil).expect(http.StatusNotFound)
	b.do("POST", base+"/restart", nil).expect(http.StatusNotFound)
	b.do("DELETE", base, map[string]any{"confirm": "one"}).expect(http.StatusNotFound)
	if _, err := e.kube.AppsV1().StatefulSets("minecraft").Get(ctx, "mc-one", metav1.GetOptions{}); err != nil {
		t.Fatal("server gone after bob's attempts")
	}

	// Admins can access any server.
	e.createUser("root", auth.RoleAdmin, false)
	e.login("root").do("GET", base, nil).expect(http.StatusOK)
}

func TestCreateValidation(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", auth.RoleUser, false)
	a := e.login("alice")
	small := e.plan("Small")

	cases := map[string]func(map[string]any){
		"no eula":       func(m map[string]any) { m["accept_eula"] = false },
		"reserved slug": func(m map[string]any) { m["slug"] = "admin" },
		"bad slug":      func(m map[string]any) { m["slug"] = "Bad_Slug" },
		"bad type":      func(m map[string]any) { m["type"] = "BUKKIT" },
		"bad version":   func(m map[string]any) { m["version"] = "latest; rm" },
		"extra setting": func(m map[string]any) { m["settings"] = map[string]any{"jvm_opts": "-Xmx64G"} },
		"bad plan":      func(m map[string]any) { m["plan_id"] = "00000000-0000-0000-0000-000000000000" },
	}
	for name, mut := range cases {
		req := createReq("ok", small)
		mut(req)
		if res := a.do("POST", "/api/servers", req); res.code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d %s", name, res.code, res.body)
		}
	}
	// Unknown top-level fields are rejected, so no image or env can be injected.
	req := createReq("ok", small)
	req["image"] = "evil/image"
	a.do("POST", "/api/servers", req).expect(http.StatusBadRequest)

	a.do("POST", "/api/servers", createReq("taken", small)).expect(http.StatusCreated)
	a.do("POST", "/api/servers", createReq("taken", small)).expect(http.StatusBadRequest)

	var avail struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	a.do("GET", "/api/servers/slug-available?slug=taken", nil).expect(http.StatusOK).decode(&avail)
	if avail.Available {
		t.Fatal("taken slug reported available")
	}
	a.do("GET", "/api/servers/slug-available?slug=fresh", nil).expect(http.StatusOK).decode(&avail)
	if !avail.Available {
		t.Fatalf("fresh slug unavailable: %s", avail.Reason)
	}
}

func TestStopStartDelete(t *testing.T) {
	e := newTestEnv(t)
	alice := e.createUser("alice", auth.RoleUser, false)
	ctx := context.Background()
	a := e.login("alice")
	small := e.plan("Small")

	var srv serverJSON
	a.do("POST", "/api/servers", createReq("world", small)).expect(http.StatusCreated).decode(&srv)
	base := "/api/servers/" + srv.ID.String()
	replicas := func() int32 {
		sts, err := e.kube.AppsV1().StatefulSets("minecraft").Get(ctx, "mc-world", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return *sts.Spec.Replicas
	}

	a.do("POST", base+"/stop", nil).expect(http.StatusOK).decode(&srv)
	if srv.DesiredState != "stopped" || replicas() != 0 {
		t.Fatal("stop did not scale down")
	}
	// The world volume survives a stop.
	if _, err := e.kube.CoreV1().PersistentVolumeClaims("minecraft").Get(ctx, "mc-world-data", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	a.do("POST", base+"/restart", nil).expect(http.StatusBadRequest)

	// Starting is blocked by the memory quota.
	if _, err := e.store.UpsertUserQuota(ctx, sqlc.UpsertUserQuotaParams{UserID: alice.ID, MaxTotalMemoryMb: pgtype.Int4{Int32: 1024, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	a.do("POST", base+"/start", nil).expect(http.StatusConflict)
	if _, err := e.store.UpsertUserQuota(ctx, sqlc.UpsertUserQuotaParams{UserID: alice.ID}); err != nil {
		t.Fatal(err)
	}
	a.do("POST", base+"/start", nil).expect(http.StatusOK)
	if replicas() != 1 {
		t.Fatal("start did not scale up")
	}

	// Settings changes are validated and applied to the template.
	a.do("PATCH", base, map[string]any{"settings": map[string]any{"difficulty": "nope"}}).expect(http.StatusBadRequest)
	a.do("PATCH", base, map[string]any{"settings": map[string]any{"difficulty": "peaceful"}}).expect(http.StatusOK).decode(&srv)
	if srv.Settings.Difficulty != "peaceful" {
		t.Fatal("settings not saved")
	}
	// Users cannot upgrade beyond their quota; downgrading storage is rejected.
	large := e.plan("Large")
	if _, err := e.store.UpsertUserQuota(ctx, sqlc.UpsertUserQuotaParams{UserID: alice.ID, MaxTotalMemoryMb: pgtype.Int4{Int32: 4096, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	a.do("PATCH", base, map[string]any{"plan_id": large.ID}).expect(http.StatusConflict)
	medium := e.plan("Medium")
	a.do("PATCH", base, map[string]any{"plan_id": medium.ID}).expect(http.StatusOK)
	a.do("PATCH", base, map[string]any{"plan_id": small.ID}).expect(http.StatusBadRequest)

	a.do("DELETE", base, map[string]any{"confirm": "wrong"}).expect(http.StatusBadRequest)
	a.do("DELETE", base, map[string]any{"confirm": "world"}).expect(http.StatusNoContent)
	a.do("GET", base, nil).expect(http.StatusNotFound)
	if _, err := e.kube.CoreV1().PersistentVolumeClaims("minecraft").Get(ctx, "mc-world-data", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("volume not deleted")
	}
}

func TestReconcilerRepairsAndDeletes(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", auth.RoleUser, false)
	ctx := context.Background()
	a := e.login("alice")
	var srv serverJSON
	a.do("POST", "/api/servers", createReq("heal", e.plan("Small"))).expect(http.StatusCreated).decode(&srv)

	// A missing service is recreated.
	if err := e.kube.CoreV1().Services("minecraft").Delete(ctx, "mc-heal", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	rec := reconcile.New(e.store, e.deps.Mgr)
	if err := rec.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.kube.CoreV1().Services("minecraft").Get(ctx, "mc-heal", metav1.GetOptions{}); err != nil {
		t.Fatal("service not recreated")
	}

	// A ready pod is reported as running.
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "mc-heal-0", Namespace: "minecraft", Labels: map[string]string{
		"app.kubernetes.io/managed-by": "mc-manager", "mc-manager/server-id": srv.ID.String(),
	}}, Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}}}
	if _, err := e.kube.CoreV1().Pods("minecraft").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Once(ctx); err != nil {
		t.Fatal(err)
	}
	a.do("GET", "/api/servers/"+srv.ID.String(), nil).expect(http.StatusOK).decode(&srv)
	if srv.Status.State != "running" {
		t.Fatalf("state: %+v", srv.Status)
	}

	// Orphans lose their workload but keep the volume.
	if err := e.store.DeleteServer(ctx, srv.ID); err != nil {
		t.Fatal(err)
	}
	if err := rec.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.kube.AppsV1().StatefulSets("minecraft").Get(ctx, "mc-heal", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatal("orphan statefulset kept")
	}
	if _, err := e.kube.CoreV1().PersistentVolumeClaims("minecraft").Get(ctx, "mc-heal-data", metav1.GetOptions{}); err != nil {
		t.Fatal("orphan volume deleted")
	}
}

func TestQuotaRace(t *testing.T) {
	e := newTestEnv(t)
	alice := e.createUser("alice", auth.RoleUser, false)
	ctx := context.Background()
	if _, err := e.store.UpsertUserQuota(ctx, sqlc.UpsertUserQuotaParams{UserID: alice.ID, MaxServers: pgtype.Int4{Int32: 1, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	a := e.login("alice")
	small := e.plan("Small")

	const n = 8
	codes := make(chan int, n)
	for i := range n {
		go func() {
			codes <- a.do("POST", "/api/servers", createReq(fmt.Sprintf("race%d", i), small)).code
		}()
	}
	created := 0
	for range n {
		if <-codes == http.StatusCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("quota of 1 allowed %d servers", created)
	}
}
