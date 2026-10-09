package api

import (
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/metrics"
)

func TestHTTPMetrics(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", auth.RoleUser, false)
	beforeFail := testutil.ToFloat64(metrics.Logins.WithLabelValues("failed"))
	beforeOK := testutil.ToFloat64(metrics.Logins.WithLabelValues("success"))
	e.client().do("POST", "/api/auth/login", map[string]string{"username": "alice", "password": "wrong-password"})
	a := e.login("alice")
	if testutil.ToFloat64(metrics.Logins.WithLabelValues("failed")) != beforeFail+1 ||
		testutil.ToFloat64(metrics.Logins.WithLabelValues("success")) != beforeOK+1 {
		t.Fatal("login counters not updated")
	}

	var srv serverJSON
	a.do("POST", "/api/servers", createReq("metrics", e.plan("Small"))).expect(http.StatusCreated).decode(&srv)
	route := testutil.ToFloat64(metrics.HTTPRequests.WithLabelValues("GET", "/api/servers/{id}", "200"))
	a.do("GET", "/api/servers/"+srv.ID.String(), nil).expect(http.StatusOK)
	// Ids are folded into the route pattern.
	if testutil.ToFloat64(metrics.HTTPRequests.WithLabelValues("GET", "/api/servers/{id}", "200")) != route+1 {
		t.Fatal("request not counted under the route pattern")
	}
	stops := testutil.ToFloat64(metrics.ServerActions.WithLabelValues("stop"))
	a.do("POST", "/api/servers/"+srv.ID.String()+"/stop", nil).expect(http.StatusOK)
	if testutil.ToFloat64(metrics.ServerActions.WithLabelValues("stop")) != stops+1 {
		t.Fatal("stop not counted")
	}
}
