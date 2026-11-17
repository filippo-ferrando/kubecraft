# Configuration

- [Where settings come from](#where-settings-come-from)
- [Core](#core)
- [Database](#database)
- [Sessions and security](#sessions-and-security)
- [Game servers](#game-servers)
- [Features](#features)
- [Kubernetes access](#kubernetes-access)

## Where settings come from

The manager reads only environment variables. With the Helm chart, they are set from the
values (see the [Chart reference](deployment/chart-reference.md)), so most installs never set
them by hand. They matter when running the manager outside the chart, for example with
`make run`.

The manager checks every value at start and stops with a list of errors if one is invalid.
Durations use Go syntax: `30s`, `15m`, `168h`.

## Core

| Variable | Default | Description |
| --- | --- | --- |
| `UI_HOST` | `localhost:8080` | Host of the UI, also used for the CSRF and WebSocket origin check |
| `SERVER_BASE_DOMAIN` | `localhost` | Servers are reachable at `<slug>.<SERVER_BASE_DOMAIN>` |
| `LISTEN_ADDR` | `:8080` | HTTP listen address of the API and UI |
| `METRICS_ADDR` | `:9090` | Prometheus listen address. An empty value disables metrics. See [Metrics](components/metrics.md). |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

## Database

| Variable | Default | Description |
| --- | --- | --- |
| `DATABASE_URL` | | Postgres URL. Can be empty when `PGHOST` is set |
| `PGHOST`, `PGUSER`, `PGPASSWORD`, `PGDATABASE`, ... | | Standard Postgres variables, used when `DATABASE_URL` is empty |

One of `DATABASE_URL` or `PGHOST` is required. The chart uses the `PG*` variables, so passwords
with characters like `/` or `@` work. Details are in [Database](components/database.md).

## Sessions and security

| Variable | Default | Description |
| --- | --- | --- |
| `SESSION_SECRET` | | Required, at least 32 characters |
| `SESSION_TTL` | `168h` | Session lifetime |
| `COOKIE_SECURE` | `true` | Set `false` only for local HTTP development |
| `ALLOWED_ORIGINS` | | Extra origins for CSRF and WebSocket checks, comma separated |
| `CLIENT_IP_HEADER` | | Header with the real client IP set by the proxy in front (for example `Cf-Connecting-Ip`). Empty uses `X-Forwarded-For`. Used for login rate limits and the audit log. |
| `BOOTSTRAP_ADMIN_USER`, `BOOTSTRAP_ADMIN_PASSWORD` | | First admin, only created when no users exist. A password change is forced at first login. |

How these are used is in [Manager and API](components/manager-api.md).

## Game servers

| Variable | Default | Description |
| --- | --- | --- |
| `MC_IMAGE`, `MC_IMAGE_TAG` | `itzg/minecraft-server`, `latest` | Image of the game servers |
| `JVM_HEAP_RATIO` | `0.75` | JVM heap as a share of the memory limit, from 0.3 to 0.95 |
| `STORAGE_CLASS` | cluster default | StorageClass of world volumes |
| `DEFAULT_MAX_SERVERS` | `2` | Starting server quota, editable later in the admin UI |

What these turn into is in [Server resources](components/server-resources.md).

## Features

| Variable | Default | Description |
| --- | --- | --- |
| `FILES_IMAGE` | | Image of the file sidecar (the chart sets the manager image). Empty disables files and backups. |
| `AUTO_SLEEP` | `false` | Tells the UI that mc-router can put servers to sleep |
| `AUTO_SLEEP_IDLE` | `15m` | Idle time before a server sleeps, shown in the UI |
| `PLAYIT_SECRET` | | Key of a self-managed playit agent. Enables public servers. |
| `PLAYIT_FIRST_PORT` | `30001` | First local port given to new tunnels |
| `PLAYIT_API` | `https://api.playit.gg` | playit API URL, only changed in tests |

See [Files and backups](components/files-and-backups.md),
[mc-router and auto sleep](components/mc-router-and-auto-sleep.md) and
[playit.gg](components/playit.md).

## Kubernetes access

| Variable | Default | Description |
| --- | --- | --- |
| `K8S_NAMESPACE` | `minecraft` | Namespace of the game servers (the chart sets the release namespace) |
| `IN_CLUSTER` | `true` | Use the pod's service account. Set `false` to use a kubeconfig. |
| `KUBECONFIG` | | Kubeconfig path when `IN_CLUSTER=false` |
