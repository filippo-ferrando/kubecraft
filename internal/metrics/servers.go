package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// States reported by kubecraft_server_state, one series per state.
var States = []string{"creating", "starting", "running", "stopping", "stopped", "error", "crashloop", "deleting", "sleeping"}

// ServerInfo is a snapshot of one server taken at scrape time.
type ServerInfo struct {
	Slug            string
	Owner           string
	Type            string
	Version         string
	Plan            string
	DesiredRunning  bool
	State           string
	RestartRequired bool
	CPULimitM       int32
	MemoryLimitMB   int32
	StorageGB       int32
	// Live values are nil when unknown (server not running or not reported yet).
	PlayersOnline *int
	PlayersMax    *int
	CPUUsageM     *int64
	MemoryUsageMB *int64
}

// Users counts accounts by role and disabled flag.
type Users struct {
	Role     string
	Disabled bool
	Count    int
}

// Source returns the current servers and users.
type Source func(ctx context.Context) ([]ServerInfo, []Users, error)

var (
	descInfo     = desc("server_info", "Static server details, always 1.", "server", "owner", "type", "version", "plan")
	descState    = desc("server_state", "1 for the current state of the server.", "server", "state")
	descDesired  = desc("server_desired_running", "1 when the server should be running.", "server")
	descRestart  = desc("server_restart_required", "1 when saved changes wait for a restart.", "server")
	descOnline   = desc("server_players_online", "Players online.", "server")
	descMax      = desc("server_players_max", "Player slots.", "server")
	descCPU      = desc("server_cpu_usage_millicores", "CPU used by the server pod.", "server")
	descMem      = desc("server_memory_usage_bytes", "Memory used by the server pod.", "server")
	descCPULim   = desc("server_cpu_limit_millicores", "CPU limit from the plan.", "server")
	descMemLim   = desc("server_memory_limit_bytes", "Memory limit from the plan.", "server")
	descStorage  = desc("server_storage_bytes", "Disk size from the plan.", "server")
	descServers  = desc("servers", "Servers by state.", "state")
	descUsers    = desc("users", "Accounts by role and disabled flag.", "role", "disabled")
	descScrapeOK = desc("server_scrape_success", "1 when server data could be read.")
)

func desc(name, help string, labels ...string) *prometheus.Desc {
	return prometheus.NewDesc(ns+"_"+name, help, labels, nil)
}

// ServerCollector reads servers at scrape time, so deleted servers leave no stale series.
type ServerCollector struct {
	src     Source
	timeout time.Duration
}

func NewServerCollector(src Source) *ServerCollector {
	return &ServerCollector{src: src, timeout: 5 * time.Second}
}

func (c *ServerCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{descInfo, descState, descDesired, descRestart, descOnline, descMax,
		descCPU, descMem, descCPULim, descMemLim, descStorage, descServers, descUsers, descScrapeOK} {
		ch <- d
	}
}

func (c *ServerCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	servers, users, err := c.src(ctx)
	if err != nil {
		slog.Warn("metrics: read servers failed", "err", err)
		ch <- prometheus.MustNewConstMetric(descScrapeOK, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(descScrapeOK, prometheus.GaugeValue, 1)

	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	counts := map[string]int{}
	for _, s := range States {
		counts[s] = 0
	}
	for _, s := range servers {
		counts[s.State]++
		gauge(descInfo, 1, s.Slug, s.Owner, s.Type, s.Version, s.Plan)
		for _, st := range States {
			gauge(descState, b2f(st == s.State), s.Slug, st)
		}
		gauge(descDesired, b2f(s.DesiredRunning), s.Slug)
		gauge(descRestart, b2f(s.RestartRequired), s.Slug)
		gauge(descCPULim, float64(s.CPULimitM), s.Slug)
		gauge(descMemLim, float64(s.MemoryLimitMB)*1024*1024, s.Slug)
		gauge(descStorage, float64(s.StorageGB)*1024*1024*1024, s.Slug)
		if s.PlayersOnline != nil {
			gauge(descOnline, float64(*s.PlayersOnline), s.Slug)
		}
		if s.PlayersMax != nil {
			gauge(descMax, float64(*s.PlayersMax), s.Slug)
		}
		if s.CPUUsageM != nil {
			gauge(descCPU, float64(*s.CPUUsageM), s.Slug)
		}
		if s.MemoryUsageMB != nil {
			gauge(descMem, float64(*s.MemoryUsageMB)*1024*1024, s.Slug)
		}
	}
	for state, n := range counts {
		gauge(descServers, float64(n), state)
	}
	for _, u := range users {
		disabled := "false"
		if u.Disabled {
			disabled = "true"
		}
		gauge(descUsers, float64(u.Count), u.Role, disabled)
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
