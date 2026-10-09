# Architecture

## Parts

```
                        ┌──────────────────────── namespace (e.g. minecraft) ─────────────────────────┐
Browser ──HTTPS──> Ingress / IngressRoute ──> manager (Go)                                            │
                        │                      ├─ API + embedded UI (:8080)                          │
                        │                      ├─ /metrics (:9090)                                   │
                        │                      ├─ reconciler (every 5s)                              │
                        │                      ├─ live poller (every 10s)                            │
                        │                      └─ talks to: Postgres, Kubernetes API, RCON, sidecars │
                        │                                                                             │
Minecraft ──TCP 25565──> mc-router (LoadBalancer) ──by hostname──> Service mc-<slug>                 │
                        │                                              └─> Pod mc-<slug>-0           │
                        │                                                   ├─ minecraft (itzg)      │
                        │                                                   └─ files (sidecar)       │
                        └─────────────────────────────────────────────────────────────────────────────┘
Optional: playit gateway pod (playit agent + gate), one local port per tunnel, see components/playit.md.
External: PostgreSQL (for example CloudNativePG), Modrinth API, Mojang version list, playit.gg API.
```

| Part | Image | Role |
|---|---|---|
| manager | `git.fferrando.cc/rdfilippo/kubecraft-manager` | API, UI, reconciler, poller, metrics |
| files sidecar | same image, started as `manager files` | File listing, editing and backups for one server |
| mc-router | `itzg/mc-router` | Routes Minecraft connections by hostname, wakes and puts servers to sleep |
| game server | `itzg/minecraft-server` | One per server, in a StatefulSet |
| Postgres | not deployed by kubecraft | Users, plans, quotas, servers, sessions, audit log |

## Where state lives

- **Postgres** holds what users asked for: owners, plans, quotas, settings, and the desired state (`running`, `stopped`, `deleting`).
- **Kubernetes** holds what is actually happening: pods, readiness, restarts. The reconciler compares the two.
- **The world** lives on a PersistentVolumeClaim per server. Nothing else in kubecraft keeps game data.
- **Secrets** per server (RCON password, file token) live in a Kubernetes Secret and are never returned by the API.
- **Memory only**: login rate limits, the last observed status, live stats. These rebuild within seconds after a restart.

## A server's life

1. **Create**: the API validates the request, locks the owner row, checks the quota and inserts the server (desired `running`). Then it creates the Secret, PVC, Service and StatefulSet. If a Kubernetes call fails, the server is marked `error` and the reconciler retries.
2. **Start up**: the itzg image downloads the server jar and any Modrinth projects, then the `mc-health` probe turns the pod ready. The reconciler reports `running`.
3. **Play**: players connect to `<slug>.<base domain>:25565`. mc-router reads the hostname from the handshake and proxies to the Service.
4. **Live data**: the poller reads the player list over RCON and CPU/RAM from metrics-server. After every new pod it also applies world settings (difficulty, game mode, PvP, whitelist) over RCON.
5. **Change**: settings saves update the database and the StatefulSet template. The StatefulSet uses `OnDelete`, so the change waits for a restart; settings that can be applied live are sent over RCON right away.
6. **Stop**: replicas go to 0. The PVC stays, so the world survives.
7. **Sleep** (optional): mc-router scales an idle server to 0 and back on the next join.
8. **Delete**: the row is marked `deleting`, cluster resources are removed, then the row. With "keep world" the PVC is detached and kept.

## Request path for the UI

1. Traefik (or another ingress) terminates TLS and forwards to the manager Service.
2. Middleware: request id, logging and metrics, panic recovery, security headers.
3. `/api/*`: session cookie lookup, CSRF check for state-changing methods, then route-level checks (logged in, password changed, admin, server owner).
4. Everything else serves the embedded single page app, with `index.html` as fallback.

## Background loops

| Loop | Interval | Does |
|---|---|---|
| Reconciler | 5s | Ensures resources, computes state, finishes deletes, cleans orphans and helper pods |
| Live poller | 10s | Player list, CPU/RAM, applies world settings once per new pod |
| Session cleanup | 10m | Removes expired sessions and stale rate limit entries |

All loops stop on SIGTERM; the HTTP server drains for up to 20 seconds.
