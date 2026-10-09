package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestServerCollector(t *testing.T) {
	online, slots := 3, 20
	cpu, mem := int64(250), int64(1024)
	servers := []ServerInfo{
		{Slug: "survival", Owner: "alice", Type: "PAPER", Version: "LATEST", Plan: "Small", DesiredRunning: true, State: "running",
			CPULimitM: 1000, MemoryLimitMB: 2048, StorageGB: 5, PlayersOnline: &online, PlayersMax: &slots, CPUUsageM: &cpu, MemoryUsageMB: &mem},
		{Slug: "creative", Owner: "bob", Type: "FABRIC", Version: "1.21.4", Plan: "Medium", State: "stopped", RestartRequired: true,
			CPULimitM: 2000, MemoryLimitMB: 4096, StorageGB: 10},
	}
	src := func(context.Context) ([]ServerInfo, []Users, error) {
		return servers, []Users{{Role: "admin", Count: 1}, {Role: "user", Count: 2}}, nil
	}
	c := NewServerCollector(src)
	want := `
# HELP kubecraft_server_players_online Players online.
# TYPE kubecraft_server_players_online gauge
kubecraft_server_players_online{server="survival"} 3
# HELP kubecraft_server_memory_usage_bytes Memory used by the server pod.
# TYPE kubecraft_server_memory_usage_bytes gauge
kubecraft_server_memory_usage_bytes{server="survival"} 1.073741824e+09
# HELP kubecraft_server_restart_required 1 when saved changes wait for a restart.
# TYPE kubecraft_server_restart_required gauge
kubecraft_server_restart_required{server="creative"} 1
kubecraft_server_restart_required{server="survival"} 0
# HELP kubecraft_users Accounts by role and disabled flag.
# TYPE kubecraft_users gauge
kubecraft_users{disabled="false",role="admin"} 1
kubecraft_users{disabled="false",role="user"} 2
`
	names := []string{"kubecraft_server_players_online", "kubecraft_server_memory_usage_bytes", "kubecraft_server_restart_required", "kubecraft_users"}
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), names...); err != nil {
		t.Fatal(err)
	}
	// One series per state, and a count of servers per state.
	if n := testutil.CollectAndCount(c, "kubecraft_server_state"); n != 2*len(States) {
		t.Fatalf("state series: %d", n)
	}
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(c)
	if _, err := reg.Gather(); err != nil {
		t.Fatalf("pedantic gather: %v", err)
	}

	// Deleted servers disappear on the next scrape.
	servers = servers[:1]
	if n := testutil.CollectAndCount(c, "kubecraft_server_info"); n != 1 {
		t.Fatalf("stale series: %d", n)
	}
}

func TestServerCollectorError(t *testing.T) {
	c := NewServerCollector(func(context.Context) ([]ServerInfo, []Users, error) { return nil, nil, errors.New("db down") })
	want := `
# HELP kubecraft_server_scrape_success 1 when server data could be read.
# TYPE kubecraft_server_scrape_success gauge
kubecraft_server_scrape_success 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
}

func TestRegistry(t *testing.T) {
	r := Registry(nil)
	HTTPRequests.WithLabelValues("GET", "/api/me", "200").Inc()
	mfs, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, mf := range mfs {
		if mf.GetName() == "kubecraft_http_requests_total" {
			found = true
		}
	}
	if !found {
		t.Fatal("http metric not registered")
	}
}
