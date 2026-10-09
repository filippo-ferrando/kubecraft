package api

import (
	"net/http"
	"testing"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
)

// TestAcceptanceFlow follows the main acceptance criteria through the API only.
func TestAcceptanceFlow(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("admin", auth.RoleAdmin, false)
	adm := e.login("admin")

	var u adminUserJSON
	adm.do("POST", "/api/admin/users", map[string]string{"username": "player1", "password": "initial-pass-1"}).expect(http.StatusCreated).decode(&u)
	if !u.MustChangePassword || u.Role != "user" {
		t.Fatalf("new user: %+v", u)
	}
	two := int32(2)
	adm.do("PUT", "/api/admin/quotas/"+u.ID.String(), quotaJSON{MaxServers: &two}).expect(http.StatusOK).decode(&u)
	if u.Limits.MaxServers == nil || *u.Limits.MaxServers != 2 {
		t.Fatalf("quota not applied: %+v", u.Limits)
	}

	p := e.client()
	p.do("POST", "/api/auth/login", map[string]string{"username": "player1", "password": "initial-pass-1"}).expect(http.StatusOK).decode(&meJSON{})
	var me meJSON
	p.do("GET", "/api/me", nil).expect(http.StatusOK).decode(&me)
	p.csrf = me.CSRFToken
	p.do("POST", "/api/me/password", map[string]string{"current_password": "initial-pass-1", "new_password": "my-own-pass-1"}).expect(http.StatusNoContent)

	small := e.plan("Small")
	p.do("POST", "/api/servers", createReq("first", small)).expect(http.StatusCreated)
	p.do("POST", "/api/servers", createReq("second", small)).expect(http.StatusCreated)
	p.do("POST", "/api/servers", createReq("third", small)).expect(http.StatusConflict)

	var all []serverJSON
	adm.do("GET", "/api/admin/servers", nil).expect(http.StatusOK).decode(&all)
	if len(all) != 2 || all[0].OwnerUsername != "player1" {
		t.Fatalf("admin servers: %+v", all)
	}
	adm.do("GET", "/api/admin/servers?owner="+u.ID.String(), nil).expect(http.StatusOK).decode(&all)
	if len(all) != 2 {
		t.Fatal("owner filter failed")
	}
	// Admins can force stop any server.
	adm.do("POST", "/api/servers/"+all[0].ID.String()+"/stop", nil).expect(http.StatusOK)
}

func TestAdminOnly(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", auth.RoleUser, false)
	a := e.login("alice")
	for _, path := range []string{"/api/admin/users", "/api/admin/plans", "/api/admin/servers", "/api/admin/audit", "/api/admin/quotas"} {
		a.do("GET", path, nil).expect(http.StatusForbidden)
	}
	a.do("POST", "/api/admin/users", map[string]string{"username": "evil", "password": "evil-password", "role": "admin"}).expect(http.StatusForbidden)
}

func TestAdminUserGuards(t *testing.T) {
	e := newTestEnv(t)
	root := e.createUser("root", auth.RoleAdmin, false)
	bob := e.createUser("bob", auth.RoleUser, false)
	adm := e.login("root")
	b := e.login("bob")

	adm.do("PATCH", "/api/admin/users/"+root.ID.String(), map[string]any{"role": "user"}).expect(http.StatusBadRequest)
	adm.do("PATCH", "/api/admin/users/"+root.ID.String(), map[string]any{"disabled": true}).expect(http.StatusBadRequest)
	adm.do("DELETE", "/api/admin/users/"+root.ID.String(), nil).expect(http.StatusBadRequest)
	adm.do("POST", "/api/admin/users", map[string]string{"username": "bob", "password": "another-pass-1"}).expect(http.StatusConflict)
	adm.do("POST", "/api/admin/users", map[string]string{"username": "x", "password": "another-pass-1"}).expect(http.StatusBadRequest)
	adm.do("POST", "/api/admin/users", map[string]string{"username": "carl", "password": "short"}).expect(http.StatusBadRequest)

	// Disabling ends the user's sessions right away.
	adm.do("PATCH", "/api/admin/users/"+bob.ID.String(), map[string]any{"disabled": true}).expect(http.StatusOK)
	b.do("GET", "/api/me", nil).expect(http.StatusUnauthorized)
	adm.do("PATCH", "/api/admin/users/"+bob.ID.String(), map[string]any{"disabled": false}).expect(http.StatusOK)

	// A reset password must be changed on next login.
	adm.do("POST", "/api/admin/users/"+bob.ID.String()+"/password", map[string]string{"password": "reset-pass-123"}).expect(http.StatusNoContent)
	b2 := e.client()
	var me meJSON
	b2.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "reset-pass-123"}).expect(http.StatusOK).decode(&me)
	if !me.User.MustChangePassword {
		t.Fatal("reset must force a password change")
	}

	// Users with servers cannot be deleted.
	b2.csrf = me.CSRFToken
	b2.do("POST", "/api/me/password", map[string]string{"current_password": "reset-pass-123", "new_password": "bob-new-pass-1"}).expect(http.StatusNoContent)
	var srv serverJSON
	b2.do("POST", "/api/servers", createReq("bobs", e.plan("Small"))).expect(http.StatusCreated).decode(&srv)
	adm.do("DELETE", "/api/admin/users/"+bob.ID.String(), nil).expect(http.StatusConflict)
	adm.do("DELETE", "/api/servers/"+srv.ID.String(), map[string]any{"confirm": "bobs"}).expect(http.StatusNoContent)
	adm.do("DELETE", "/api/admin/users/"+bob.ID.String(), nil).expect(http.StatusNoContent)
}

func TestAdminPlans(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("root", auth.RoleAdmin, false)
	e.createUser("alice", auth.RoleUser, false)
	adm := e.login("root")

	bad := map[string]any{"name": "Tiny", "cpu_request_m": 500, "cpu_limit_m": 100, "memory_limit_mb": 2048, "storage_gb": 5, "active": true}
	adm.do("POST", "/api/admin/plans", bad).expect(http.StatusBadRequest)
	var p planJSON
	good := map[string]any{"name": "Huge", "cpu_request_m": 4000, "cpu_limit_m": 8000, "memory_limit_mb": 16384, "storage_gb": 50, "active": false}
	adm.do("POST", "/api/admin/plans", good).expect(http.StatusCreated).decode(&p)
	adm.do("POST", "/api/admin/plans", good).expect(http.StatusConflict)

	// Inactive plans are hidden from users and cannot be used.
	a := e.login("alice")
	var plans []planJSON
	a.do("GET", "/api/plans", nil).expect(http.StatusOK).decode(&plans)
	for _, x := range plans {
		if x.ID == p.ID {
			t.Fatal("inactive plan listed")
		}
	}
	a.do("POST", "/api/servers", createReq("huge", p)).expect(http.StatusBadRequest)

	adm.do("PATCH", "/api/admin/plans/"+p.ID.String(), map[string]any{"active": true}).expect(http.StatusOK).decode(&p)
	if !p.Active || p.Name != "Huge" {
		t.Fatalf("partial update: %+v", p)
	}
	var srv serverJSON
	a.do("POST", "/api/servers", createReq("huge", p)).expect(http.StatusCreated).decode(&srv)
	adm.do("DELETE", "/api/admin/plans/"+p.ID.String(), nil).expect(http.StatusConflict)
	adm.do("PATCH", "/api/admin/plans/"+p.ID.String(), map[string]any{"storage_gb": 10}).expect(http.StatusBadRequest)

	var entries []auditJSON
	adm.do("GET", "/api/admin/audit?limit=5", nil).expect(http.StatusOK).decode(&entries)
	if len(entries) == 0 || entries[0].Action != "server.create" || entries[0].Username != "alice" {
		t.Fatalf("audit: %+v", entries)
	}
	var older []auditJSON
	adm.do("GET", "/api/admin/audit?before="+itoa(entries[0].ID), nil).expect(http.StatusOK).decode(&older)
	if len(older) == 0 || older[0].ID >= entries[0].ID {
		t.Fatal("audit pagination broken")
	}
}

func TestDefaultQuota(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("root", auth.RoleAdmin, false)
	e.createUser("alice", auth.RoleUser, false)
	adm := e.login("root")
	one := int32(1)
	adm.do("PUT", "/api/admin/quotas/default", quotaJSON{MaxServers: &one}).expect(http.StatusOK)
	neg := int32(-1)
	adm.do("PUT", "/api/admin/quotas/default", quotaJSON{MaxServers: &neg}).expect(http.StatusBadRequest)

	a := e.login("alice")
	a.do("POST", "/api/servers", createReq("one", e.plan("Small"))).expect(http.StatusCreated)
	a.do("POST", "/api/servers", createReq("two", e.plan("Small"))).expect(http.StatusConflict)
}
