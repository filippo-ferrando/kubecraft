package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/config"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/k8s"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/live"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/modrinth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/servers"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/testutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const testPassword = "test-password-123"

type testEnv struct {
	t     *testing.T
	store *db.Store
	srv   *httptest.Server
	deps  Deps
	kube  *fake.Clientset
	// rconCmds receives every command sent to the fake RCON server.
	rconCmds chan string
}

// newTestEnv starts the API against a fresh database. opts can tweak deps before start.
func newTestEnv(t *testing.T, opts ...func(*Deps)) *testEnv {
	t.Helper()
	store := testutil.DB(t)
	cfg := &config.Config{
		UIHost:           "mine.test",
		ServerBaseDomain: "mine.test",
		Namespace:        "minecraft",
		MCImage:          "itzg/minecraft-server",
		MCImageTag:       "latest",
		JVMHeapRatio:     0.75,
		SessionSecret:    []byte(strings.Repeat("s", 32)),
		SessionTTL:       time.Hour,
		CookieSecure:     false,
		DefaultMaxServer: 2,
	}
	kube := fake.NewClientset()
	kc := k8s.New(kube, k8s.Options{
		Namespace:  cfg.Namespace,
		Image:      cfg.MCImage + ":" + cfg.MCImageTag,
		HeapRatio:  cfg.JVMHeapRatio,
		BaseDomain: cfg.ServerBaseDomain,
	})
	cmds := make(chan string, 100)
	rconAddr := testutil.RCONServer(t, "secret", func(cmd string) string {
		select {
		case cmds <- cmd:
		default:
		}
		if cmd == "list" {
			return "There are 0 of a max of 20 players online: "
		}
		return "ok: " + cmd
	})
	pool := live.NewPool(func(string) string { return rconAddr }, func(context.Context, string) (string, error) { return "secret", nil })
	t.Cleanup(pool.Close)
	catalog := modrinth.New(testutil.FakeModrinth(t, nil), "kubecraft-test")
	mgr := servers.NewManager(store, kc)
	mgr.SetContentValidator(catalog)
	d := Deps{
		Catalog: catalog,
		RCON:    pool,
		Config:  cfg,
		Store:   store,
		Auth:    auth.NewService(store, auth.LocalAuthenticator{Store: store}, cfg.SessionSecret, cfg.SessionTTL),
		Mgr:     mgr,
	}
	for _, o := range opts {
		o(&d)
	}
	srv := httptest.NewServer(New(d).Handler())
	t.Cleanup(srv.Close)
	return &testEnv{t: t, store: store, srv: srv, deps: d, kube: kube, rconCmds: cmds}
}

// createUser inserts a user directly in the database.
func (e *testEnv) createUser(name, role string, mustChange bool) sqlc.User {
	e.t.Helper()
	u, err := auth.CreateUser(context.Background(), e.store.Queries, name, testPassword, role, mustChange)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// client is a browser-like client with a cookie jar and the CSRF token.
type client struct {
	t      *testing.T
	base   string
	http   *http.Client
	csrf   string
	origin string
}

func (e *testEnv) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: e.t, base: e.srv.URL, http: &http.Client{Jar: jar}, origin: "http://mine.test"}
}

// login logs in and fails the test on error.
func (e *testEnv) login(name string) *client {
	c := e.client()
	res := c.do("POST", "/api/auth/login", map[string]string{"username": name, "password": testPassword})
	res.expect(http.StatusOK)
	var me meJSON
	res.decode(&me)
	c.csrf = me.CSRFToken
	return c
}

type response struct {
	t    *testing.T
	code int
	body []byte
	hdr  http.Header
}

func (c *client) do(method, path string, body any) *response {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	if c.csrf != "" {
		req.Header.Set(csrfHeader, c.csrf)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return &response{t: c.t, code: res.StatusCode, body: b, hdr: res.Header}
}

func (r *response) expect(code int) *response {
	r.t.Helper()
	if r.code != code {
		r.t.Fatalf("expected status %d, got %d: %s", code, r.code, r.body)
	}
	return r
}

func (r *response) decode(v any) {
	r.t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		r.t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (r *response) errorMsg() string {
	var e errorBody
	_ = json.Unmarshal(r.body, &e)
	return e.Error
}

func sqlcFlags(id uuid.UUID, role string, disabled, console bool) sqlc.UpdateUserFlagsParams {
	return sqlc.UpdateUserFlagsParams{ID: id, Role: role, Disabled: disabled, ConsoleEnabled: console}
}

func sqlcAuditParams(limit int32) sqlc.ListAuditParams {
	return sqlc.ListAuditParams{Limit: limit}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func envMapAPI(env []corev1.EnvVar) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		m[e.Name] = e.Value
	}
	return m
}
