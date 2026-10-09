# Metrics

Code: `internal/metrics`, metrics source in `cmd/manager/main.go`.

The manager serves Prometheus metrics on `METRICS_ADDR` (default `:9090`, path `/metrics`). This port is not the UI port, so the ingress never exposes it.

## How per-server series are built

A collector reads servers, plans and users at scrape time, and adds the live numbers from memory. Deleted servers vanish on the next scrape; there is no stale series cleanup to get wrong. If the database cannot be read, `kubecraft_server_scrape_success` is 0.

## Series

Per server (label `server` is the slug): `server_info` (owner, type, version, plan), `server_state` (one series per state, 1 for the current one), `server_desired_running`, `server_restart_required`, `server_players_online`, `server_players_max`, `server_cpu_usage_millicores`, `server_memory_usage_bytes`, `server_cpu_limit_millicores`, `server_memory_limit_bytes`, `server_storage_bytes`.

Totals: `servers{state}`, `users{role,disabled}`.

Manager: `http_requests_total` and `http_request_duration_seconds` by route pattern (ids never appear in labels; WebSockets are left out of latency), `logins_total{result}`, `server_actions_total{action}`, `quota_rejections_total`, `console_commands_total{result}`, `rcon_commands_total{result}`, `websocket_connections{kind}`, `reconcile_runs_total{result}`, `reconcile_duration_seconds`, `reconcile_apply_errors_total`, `playit_syncs_total{result}`, `playit_tunnel_actions_total{action,result}`, plus Go and process metrics.

All names start with `kubecraft_`.

## Scraping

The chart creates `<release>-metrics` (ClusterIP) and, with `metrics.serviceMonitor.enabled`, a ServiceMonitor. With `mcRouter.metrics: true`, mc-router's own metrics are scraped through a second internal Service (never through its LoadBalancer). `networkPolicy.metricsFrom` decides which namespaces may scrape.

## Example alerts

```
kubecraft_server_state{state=~"crashloop|error"} == 1
kubecraft_server_memory_usage_bytes / kubecraft_server_memory_limit_bytes > 0.95
increase(kubecraft_reconcile_apply_errors_total[15m]) > 0
kubecraft_server_scrape_success == 0
```
