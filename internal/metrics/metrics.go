// Package metrics defines the Prometheus metrics of the manager.
package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const ns = "kubecraft"

var (
	HTTPRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "http_requests_total", Help: "HTTP requests by route pattern and status code.",
	}, []string{"method", "route", "code"})

	HTTPDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns, Name: "http_request_duration_seconds", Help: "HTTP request latency, WebSockets excluded.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"method", "route"})

	Logins = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "logins_total", Help: "Login attempts by result (success, failed, rate_limited).",
	}, []string{"result"})

	ServerActions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "server_actions_total", Help: "Successful server lifecycle actions.",
	}, []string{"action"})

	QuotaRejections = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: ns, Name: "quota_rejections_total", Help: "Requests refused because of a quota.",
	})

	ConsoleCommands = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "console_commands_total", Help: "Commands typed in the web console by result.",
	}, []string{"result"})

	RCONCommands = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "rcon_commands_total", Help: "RCON commands sent by the manager by result (ok, unavailable, rejected).",
	}, []string{"result"})

	WebSockets = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: ns, Name: "websocket_connections", Help: "Open log and console sockets.",
	}, []string{"kind"})

	ReconcileRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "reconcile_runs_total", Help: "Reconcile passes by result.",
	}, []string{"result"})

	ReconcileDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: ns, Name: "reconcile_duration_seconds", Help: "Duration of a reconcile pass.",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 12),
	})

	ReconcileApplyErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: ns, Name: "reconcile_apply_errors_total", Help: "Failed attempts to create or update server resources.",
	})
)

// Registry returns a registry with all manager metrics, Go and process metrics,
// and the per-server collector.
func Registry(src Source) *prometheus.Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		HTTPRequests, HTTPDuration, Logins, ServerActions, QuotaRejections, ConsoleCommands,
		RCONCommands, WebSockets, ReconcileRuns, ReconcileDuration, ReconcileApplyErrors,
	)
	if src != nil {
		r.MustRegister(NewServerCollector(src))
	}
	return r
}

// Serve exposes /metrics on addr until ctx ends.
func Serve(ctx context.Context, addr string, reg *prometheus.Registry) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	// Shutdown needs a fresh context because ctx is already done here.
	go func() { //nolint:gosec
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("metrics listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("metrics server failed", "err", err)
	}
}
