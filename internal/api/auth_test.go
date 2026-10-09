package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
)

func TestHealth(t *testing.T) {
	e := newTestEnv(t)
	c := e.client()
	c.do("GET", "/healthz", nil).expect(http.StatusOK)
	c.do("GET", "/readyz", nil).expect(http.StatusOK)
}

func TestReadyzHidesErrorDetail(t *testing.T) {
	e := newTestEnv(t, func(d *Deps) {
		d.Ready = func(context.Context) error { return errors.New("secret internal detail") }
	})
	res := e.client().do("GET", "/readyz", nil).expect(http.StatusServiceUnavailable)
	if msg := res.errorMsg(); msg != "not ready" {
		t.Fatalf("readyz leaked %q", msg)
	}
}

func TestLoginFlow(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", auth.RoleUser, false)

	anon := e.client()
	anon.do("GET", "/api/me", nil).expect(http.StatusUnauthorized)

	bad := anon.do("POST", "/api/auth/login", map[string]string{"username": "alice", "password": "wrong-password"})
	bad.expect(http.StatusUnauthorized)
	unknown := anon.do("POST", "/api/auth/login", map[string]string{"username": "nobody", "password": "wrong-password"})
	if unknown.expect(http.StatusUnauthorized).errorMsg() != bad.errorMsg() {
		t.Fatal("unknown user and wrong password must look the same")
	}

	c := e.login("Alice ")
	var me meJSON
	c.do("GET", "/api/me", nil).expect(http.StatusOK).decode(&me)
	if me.User.Username != "alice" || me.User.Role != "user" || me.CSRFToken == "" {
		t.Fatalf("unexpected me: %+v", me)
	}

	c.do("POST", "/api/auth/logout", nil).expect(http.StatusNoContent)
	c.do("GET", "/api/me", nil).expect(http.StatusUnauthorized)
}

func TestDisabledUserCannotLogin(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser("bob", auth.RoleUser, false)
	c := e.login("bob")
	if _, err := e.store.UpdateUserFlags(context.Background(), sqlcFlags(u.ID, u.Role, true, true)); err != nil {
		t.Fatal(err)
	}
	c.do("GET", "/api/me", nil).expect(http.StatusUnauthorized)
	e.client().do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": testPassword}).expect(http.StatusUnauthorized)
}

func TestLoginRateLimit(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("carol", auth.RoleUser, false)
	c := e.client()
	for i := 0; i < 5; i++ {
		c.do("POST", "/api/auth/login", map[string]string{"username": "carol", "password": "nope-nope-nope"}).expect(http.StatusUnauthorized)
	}
	res := c.do("POST", "/api/auth/login", map[string]string{"username": "carol", "password": testPassword})
	res.expect(http.StatusTooManyRequests)
	if res.hdr.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func TestCSRF(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("dave", auth.RoleUser, false)
	c := e.login("dave")

	token := c.csrf
	c.csrf = ""
	c.do("POST", "/api/auth/logout", nil).expect(http.StatusForbidden)
	c.csrf = "wrong"
	c.do("POST", "/api/auth/logout", nil).expect(http.StatusForbidden)

	c.csrf = token
	c.origin = "https://evil.example"
	c.do("POST", "/api/auth/logout", nil).expect(http.StatusForbidden)

	anon := e.client()
	anon.origin = "https://evil.example"
	anon.do("POST", "/api/auth/login", map[string]string{"username": "dave", "password": testPassword}).expect(http.StatusForbidden)
}

func TestForcedPasswordChange(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("admin", auth.RoleAdmin, true)
	c := e.login("admin")
	other := e.login("admin")

	// A protected route is blocked until the password is changed.
	c.do("GET", "/api/plans", nil).expect(http.StatusForbidden)

	c.do("POST", "/api/me/password", map[string]string{"current_password": "wrong-password", "new_password": "new-password-123"}).expect(http.StatusBadRequest)
	c.do("POST", "/api/me/password", map[string]string{"current_password": testPassword, "new_password": "short"}).expect(http.StatusBadRequest)
	c.do("POST", "/api/me/password", map[string]string{"current_password": testPassword, "new_password": "new-password-123"}).expect(http.StatusNoContent)

	var me meJSON
	c.do("GET", "/api/me", nil).expect(http.StatusOK).decode(&me)
	if me.User.MustChangePassword {
		t.Fatal("flag not cleared")
	}
	// Other sessions are ended by the password change.
	other.do("GET", "/api/me", nil).expect(http.StatusUnauthorized)
}

func TestBootstrap(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	if err := auth.Bootstrap(ctx, e.store, "root", "bootstrap-pass-1"); err != nil {
		t.Fatal(err)
	}
	if err := auth.Bootstrap(ctx, e.store, "other", "bootstrap-pass-2"); err != nil {
		t.Fatal(err)
	}
	users, err := e.store.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Username != "root" || users[0].Role != auth.RoleAdmin || !users[0].MustChangePassword {
		t.Fatalf("unexpected users: %+v", users)
	}
}
