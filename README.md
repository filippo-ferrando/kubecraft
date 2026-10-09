# kubecraft

Multi-user Minecraft server manager for Kubernetes (k3s). Users create and run their own servers from a web UI. Each server is a StatefulSet in the cluster, and [mc-router](https://github.com/itzg/mc-router) routes players to it by hostname on port 25565.

- One container image: Go backend with the Svelte UI embedded.
- Roles: `admin` and `user`. Users only see and control their own servers.
- Per-user quotas (servers, RAM, CPU, disk) and admin-managed plans.
- Plugin servers (Paper, Purpur) and modded servers (Fabric, Forge, NeoForge) with a searchable Modrinth catalog.
- Live logs, an RCON console in the browser, player management, CPU/RAM usage.
- Auto sleep: idle servers stop and start again when a player joins.
- World and full-server backup downloads, and an editor for plugin and mod config files.
- Helm chart in `charts/kubecraft`, ready for ArgoCD.

Full documentation, including design decisions and a deployment guide with example files, is in [docs/](docs/README.md).

## Architecture

```
Browser --HTTPS--> Ingress (UI host) --> manager (Go: API + UI)
                                           |-- PostgreSQL (users, plans, quotas, servers, audit)
                                           '-- Kubernetes API (namespaced Role only)

Minecraft client --TCP 25565--> mc-router --(hostname)--> Service mc-<slug> --> StatefulSet mc-<slug>
```

- Postgres is the source of truth for ownership, plans, and quotas. Kubernetes is the source of truth for runtime state.
- A reconciler runs every 5 seconds. It recreates missing resources, applies config drift, reports state (`creating`, `starting`, `running`, `stopping`, `stopped`, `error`, `crashloop`), finishes pending deletes, and removes resources of servers that no longer exist (their volumes are kept).
- A poller runs every 10 seconds for running servers. It reads players over RCON (`list`) and CPU/RAM from metrics-server.
- Only the manager talks to the Kubernetes API, with its own ServiceAccount and a namespaced Role. The browser never talks to the cluster.

### Resources per server

All resources live in the release namespace and are named `mc-<slug>`:

| Resource | Purpose |
|---|---|
| Secret `mc-<slug>-rcon` | Generated RCON password, never returned by the API |
| PVC `mc-<slug>-data` | World data, size from the plan, only grows |
| StatefulSet `mc-<slug>` | `itzg/minecraft-server`, 1 replica when running, 0 when stopped |
| Service `mc-<slug>` | ClusterIP, ports 25565 and 25575 (RCON stays inside the cluster) |

Labels on everything: `app.kubernetes.io/managed-by=mc-manager`, `mc-manager/server-id`, `mc-manager/owner-id`, `mc-manager/slug`.

Server pods run as UID 1000, non-root, with all capabilities dropped, no service account token and no host mounts. Memory request equals the limit, and the JVM heap (`MEMORY`) is `JVM_HEAP_RATIO` (default 0.75) of the limit.

### Settings and restarts

Users can only change a fixed list of settings: MOTD, difficulty, game mode, max players, view distance, whitelist, ops, PvP, seed, world type, online mode. Unknown fields are rejected, so no image, env var, or volume can be injected.

- Difficulty, game mode, PvP, whitelist on/off, whitelist entries and ops are applied over RCON right away and again after every start. They do not need a restart.
- MOTD, max players, view distance, online mode, version and plan changes apply on the next restart (the StatefulSet uses `OnDelete`). The UI shows a "restart required" banner.
- Seed and world type only apply when a world is generated.

### Mods and plugins

The wizard groups server types into Vanilla, Plugins (Paper, Purpur) and Mods (Fabric, Forge, NeoForge). For plugin and mod types, a catalog step searches [Modrinth](https://modrinth.com) through the manager (`/api/catalog/search`). Results are filtered to server-side projects for the chosen loader and game version. The same picker is on the server page (Mods or Plugins tab).

Only the chosen Modrinth slugs are stored, in the server settings. They are passed to the image as `MODRINTH_PROJECTS` with `MODRINTH_DOWNLOAD_DEPENDENCIES=required`, and the server downloads them on start. Nothing is mirrored or stored by kubecraft. Choices are checked against the Modrinth API when saved (the project exists, runs on servers, supports the loader). Changes apply on the next restart.

The manager needs outbound HTTPS to `api.modrinth.com`, the browser loads icons from `cdn.modrinth.com`, and server pods download from Modrinth on start. CurseForge is not used because its API needs a key.

### Auto sleep

With `mcRouter.autoScale.enabled` (default on), mc-router runs with `--auto-scale-up --auto-scale-down`. Each server opts in with its "Auto sleep" setting:

- An idle server (no players for `mcRouter.autoScale.downAfter`, default 15m) is scaled to 0 and shown as `sleeping`. Pinging it shows "<name> is sleeping, join to wake it up".
- The first join attempt starts it. The client is disconnected while the server boots, the player joins again after about 30 seconds. "Wake up" in the UI does the same.
- The reconciler leaves the replica count of sleeping servers to mc-router. Stopped servers get `autoScaleUp=false`, so a connection never starts a server its owner stopped.
- Quota counts a sleeping server as running, since it can wake at any time.

### Files and backups

Every server pod has a small `files` sidecar: the manager image started as `manager files`, sharing `/data`, with a per-server token from the `mc-<slug>-rcon` Secret. Only the manager can reach it (NetworkPolicy, port 8081). All paths are confined to `/data` with Go's `os.Root`, so `..` and symlinks cannot escape.

- **Backups** (Backups tab): "Download world" streams a `.tar.gz` of the world folders, player lists and `server.properties`. "Download full server" adds plugins, mods and configs, without the server jar and caches. On a running server the manager sends `save-off` and `save-all flush` first and `save-on` after, so the archive is consistent.
- **Files** (Files tab): browse `/data` and edit existing text config files (`.yml`, `.json`, `.toml`, `.properties`, `.conf` and similar, up to 1 MB). Writes are atomic. Changes apply on restart or plugin reload.
- When the server is stopped or sleeping, the manager starts a temporary pod `mc-<slug>-files` that mounts the volume. It ends by itself after 30 minutes and the reconciler removes it as soon as the server pod comes back.

The manager Role gets `create` on `pods` for that helper pod. No `pods/exec` is used. Set `files.enabled: false` to remove the sidecar.

### Deleting servers

Deleting asks for the server name. The server is marked `deleting`, then its resources and the database row are removed. If a step fails, the reconciler finishes it later. With "keep world", the volume is detached and labelled `mc-manager/retained-from=<slug>`. The slug stays reserved until an admin deletes that PVC.

## Access and DNS

Two hosts are needed:

- The UI host (`UI_HOST`), served through an Ingress or Traefik IngressRoute with TLS.
- A wildcard for servers, `*.<SERVER_BASE_DOMAIN>`, pointing at the address of the mc-router Service (TCP 25565).

Minecraft traffic is raw TCP. If the DNS provider is Cloudflare, the wildcard record must be **DNS only (grey cloud)**, never proxied. Cloudflare Tunnels cannot carry it either.

### Example: Tailscale only

For a homelab where Minecraft must only be reachable from tailnet members:

- `manager.uiHost: mine.internal.fferrando.cc` and `manager.serverBaseDomain: mine.internal.fferrando.cc`.
- The local DNS (for example an AdGuard rewrite for `*.internal.fferrando.cc`, served to tailnet clients) resolves both names to a node IP. No public DNS record is created.
- mc-router uses a `LoadBalancer` Service (k3s servicelb), so port 25565 listens on the nodes. Nothing forwards 25565 from the internet.
- Players connect to `<slug>.mine.internal.fferrando.cc`.

## Install

### 1. Database

Any PostgreSQL 14+ works. With CloudNativePG, create a role and database, then point the chart at the role secret (keys `username` and `password`):

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: DatabaseRole
metadata: {name: kubecraft-user, namespace: database}
spec:
  cluster: {name: homelab-db}
  name: kubecraft
  login: true
  passwordSecret: {name: kubecraft-db-credentials}
---
apiVersion: postgresql.cnpg.io/v1
kind: Database
metadata: {name: kubecraft, namespace: database}
spec:
  name: kubecraft
  owner: kubecraft
  cluster: {name: homelab-db}
```

The chart passes `PGHOST`, `PGUSER`, `PGPASSWORD` and so on, so passwords with special characters work. A full URL in a secret (`database.urlSecret`) is also supported. Migrations run on start.

If the database namespace has a NetworkPolicy, allow the kubecraft namespace.

### 2. Secrets

The manager needs `SESSION_SECRET` (at least 32 characters) and, for the first start, a bootstrap admin:

```bash
kubectl create secret generic kubecraft-secrets -n minecraft \
  --from-literal=SESSION_SECRET="$(openssl rand -hex 32)" \
  --from-literal=BOOTSTRAP_ADMIN_USER=admin \
  --from-literal=BOOTSTRAP_ADMIN_PASSWORD="$(openssl rand -base64 18)" \
  --dry-run=client -o yaml | kubeseal --format yaml > kubecraft-secrets.yaml
```

The bootstrap admin is only created when no users exist, and must change the password on first login.

### 3. Values

```yaml
image:
  repository: git.fferrando.cc/rdfilippo/kubecraft
  tag: "0.1.0"
manager:
  uiHost: mine.internal.fferrando.cc
  serverBaseDomain: mine.internal.fferrando.cc
  storageClass: longhorn
  defaultMaxServers: 2
secrets:
  existingSecret: kubecraft-secrets
database:
  host: homelab-db-rw.database.svc
  name: kubecraft
  credentialsSecret:
    name: kubecraft-db-credentials
ingressRoute:
  enabled: true
  entryPoints: [websecure]
  tls: {secretName: kubecraft-tls}
certificate:
  enabled: true
  secretName: kubecraft-tls
  issuerRef: {name: letsencrypt-cloudflare, kind: ClusterIssuer}
networkPolicy:
  managerFrom:
    - namespaceSelector:
        matchLabels: {kubernetes.io/metadata.name: traefik}
```

See `charts/kubecraft/values.yaml` for every option (mc-router Service type, ResourceQuota, LimitRange, NetworkPolicies, resources).

### 4. ArgoCD Application

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: kubecraft
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://git.fferrando.cc/rdfilippo/kubecraft.git
    targetRevision: main
    path: charts/kubecraft
    helm:
      valueFiles: [values-homelab.yaml]
  destination:
    server: https://kubernetes.default.svc
    namespace: minecraft
  syncPolicy:
    automated: {prune: true, selfHeal: true}
    syncOptions: [CreateNamespace=true]
```

Server resources are created by the manager, not by ArgoCD, so they do not show up as drift.

### What the chart creates

- Manager Deployment, Service, ServiceAccount, namespaced Role and RoleBinding. The Role covers `statefulsets`, `services`, `persistentvolumeclaims`, `secrets`, `configmaps`, `pods` (and `pods/log`), read-only `events`, and read-only `pods.metrics.k8s.io`. No ClusterRole.
- mc-router Deployment with `--in-kube-cluster`, watching only its namespace. Its Role reads Services and, with auto sleep, patches StatefulSets. Exposed by a `LoadBalancer` Service on 25565 (type configurable).
- ResourceQuota and LimitRange as a backstop for the whole namespace.
- NetworkPolicies: the manager only from the ingress namespace, mc-router open on 25565, game servers only from mc-router and the manager, RCON only from the manager.
- Optional Ingress, Traefik IngressRoute, and cert-manager Certificate.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | | Postgres URL. Can be empty when `PGHOST` and the other `PG*` variables are set |
| `UI_HOST` | `localhost:8080` | Host of the UI, also used for the CSRF and WebSocket origin check |
| `SERVER_BASE_DOMAIN` | `localhost` | Servers get `<slug>.<SERVER_BASE_DOMAIN>` |
| `K8S_NAMESPACE` | `minecraft` | Namespace of servers (the chart sets the release namespace) |
| `STORAGE_CLASS` | cluster default | Storage class of world volumes |
| `MC_IMAGE` / `MC_IMAGE_TAG` | `itzg/minecraft-server` / `latest` | Server image |
| `JVM_HEAP_RATIO` | `0.75` | Heap as a share of the memory limit (0.3 to 0.95) |
| `SESSION_SECRET` | | Required, at least 32 characters |
| `SESSION_TTL` | `168h` | Session lifetime |
| `BOOTSTRAP_ADMIN_USER` / `BOOTSTRAP_ADMIN_PASSWORD` | | First admin, only used when no users exist |
| `DEFAULT_MAX_SERVERS` | `2` | Initial default quota, editable later in the admin UI |
| `COOKIE_SECURE` | `true` | Set `false` only for local HTTP development |
| `ALLOWED_ORIGINS` | | Extra origins for CSRF and WebSocket checks, comma separated |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `FILES_IMAGE` | | Image of the file sidecar (the chart sets the manager image), empty disables files and backups |
| `AUTO_SLEEP` / `AUTO_SLEEP_IDLE` | `false` / `15m` | Whether mc-router auto scales, and its idle time (shown in the UI) |
| `METRICS_ADDR` | `:9090` | Prometheus listen address, set to an empty value to disable |
| `IN_CLUSTER` / `KUBECONFIG` | `true` | Outside a cluster, set `IN_CLUSTER=false` to use a kubeconfig |

## Metrics

The manager serves Prometheus metrics on a separate port (`METRICS_ADDR`, default `:9090`, path `/metrics`). The ingress never routes to it. The chart creates a `<release>-metrics` ClusterIP Service, an optional ServiceMonitor, and a NetworkPolicy rule for the scraper namespace:

```yaml
metrics:
  serviceMonitor:
    enabled: true
    labels: {release: kube-prometheus-stack}
networkPolicy:
  metricsFrom:
    - namespaceSelector:
        matchLabels: {kubernetes.io/metadata.name: monitoring}
mcRouter:
  metrics: true   # also scrape mc-router (connections, logins per server)
```

| Metric | Labels | Meaning |
|---|---|---|
| `kubecraft_server_info` | server, owner, type, version, plan | Always 1, for joins |
| `kubecraft_server_state` | server, state | 1 for the current state |
| `kubecraft_server_desired_running` | server | 1 when it should run |
| `kubecraft_server_restart_required` | server | 1 when changes wait for a restart |
| `kubecraft_server_players_online` / `_players_max` | server | From RCON `list` |
| `kubecraft_server_cpu_usage_millicores` / `_memory_usage_bytes` | server | From metrics-server |
| `kubecraft_server_cpu_limit_millicores` / `_memory_limit_bytes` / `_storage_bytes` | server | From the plan |
| `kubecraft_servers` | state | Server count per state |
| `kubecraft_users` | role, disabled | Account count |
| `kubecraft_server_scrape_success` | | 0 when the database could not be read |
| `kubecraft_http_requests_total` / `_http_request_duration_seconds` | method, route, code | API traffic by route pattern |
| `kubecraft_logins_total` | result | success, failed, rate_limited |
| `kubecraft_server_actions_total` | action | create, start, stop, restart, update, delete |
| `kubecraft_quota_rejections_total` | | Requests refused by a quota |
| `kubecraft_console_commands_total` | result | Web console commands |
| `kubecraft_rcon_commands_total` | result | ok, unavailable, rejected |
| `kubecraft_websocket_connections` | kind | Open logs and console sockets |
| `kubecraft_reconcile_runs_total` / `_reconcile_duration_seconds` / `_reconcile_apply_errors_total` | | Reconciler health |

Go runtime and process metrics are included. Per-server series are read at scrape time, so deleted servers do not leave stale series.

Example alerts: `kubecraft_server_state{state=~"crashloop|error"} == 1`, `kubecraft_server_memory_usage_bytes / kubecraft_server_memory_limit_bytes > 0.95`, `increase(kubecraft_reconcile_apply_errors_total[15m]) > 0`.

## Security

- Passwords are hashed with argon2id. Sessions are stored in Postgres as an HMAC of the cookie token, delivered as `HttpOnly`, `SameSite=Lax` (and `Secure`) cookies.
- State-changing requests need the session CSRF token in `X-CSRF-Token` and an allowed `Origin`. WebSockets check the origin too.
- Logins are rate limited per user and per IP. Unknown users and wrong passwords give the same answer.
- Every server route checks that the user owns the server or is an admin. Other users' servers return 404.
- Admins can disable the console per user. Every console command and admin action goes into the audit log.
- Admins cannot disable, demote, or delete themselves, and the last active admin is protected.
- The auth layer uses an `Authenticator` interface, so OIDC can be added as another provider.

## Development

Requirements: Go 1.27, Node 24+, Docker. sqlc, golangci-lint, helm and Postgres run in Docker through the Makefile.

```bash
make test-unit         # Go unit tests (integration tests skip without a database)
make test-integration  # starts Postgres in Docker, runs every Go test with -race
make test-web          # svelte-check and vitest
make e2e               # k3s in Docker, chart install, real Paper server, ping through mc-router
make lint              # golangci-lint
make chart-lint        # helm lint (in Docker)
make chart-lint-local  # helm lint with a local helm, plus the "no database, no render" check
make sqlc              # regenerate queries after editing internal/db/queries
make run               # manager on :8080 against the test database and the current kubeconfig
make docker            # build the image
```

For UI work, run `make run` and `cd web && npm run dev`. Vite proxies `/api` (including WebSockets) to `:8080`, and `ALLOWED_ORIGINS` in `make run` already accepts `http://localhost:5173`.

### CI and releases

Forgejo Actions in `.forgejo/workflows`:

- `ci.yaml`, on pushes to main and pull requests: gofmt, vet, golangci-lint, Go tests (with a Postgres service, so integration tests run), svelte-check, vitest, web build, chart lint, and an image build without push.
- `release.yaml`, on a tag like `v0.2.0`: runs the tests again, pushes `git.internal.fferrando.cc/rdfilippo/kubecraft` with the tags `v0.2.0`, `0.2.0`, `0.2` and `latest` (pre-releases like `v0.3.0-rc1` only get their own tag), pushes the Helm chart to `oci://git.internal.fferrando.cc/rdfilippo/kubecraft` with version `0.2.0`, and publishes a release with the commits since the previous tag.

The packaged chart uses the tag as `appVersion`, so its default manager and file sidecar images are the released ones. The repository secret `LOCAL_TOKEN` must hold an access token with `write:package`. `make e2e` needs a privileged Docker daemon and stays a manual check before tagging.

```bash
git tag v0.2.0 && git push origin v0.2.0
```

### Layout

```
cmd/manager          entry point
internal/api         HTTP handlers, middleware, WebSockets
internal/auth        passwords, sessions, login rate limit, bootstrap
internal/config      environment config
internal/db          migrations, sqlc queries and generated code
internal/k8s         resource builders, ensure/scale/delete, state mapping, logs
internal/files       file sidecar: listing, editing, tar.gz backups
internal/live        RCON pool, player list parsing, stats poller
internal/metrics     Prometheus metrics and the per-server collector
internal/modrinth    Modrinth catalog search and validation
internal/quota       quota math
internal/reconcile   reconciler
internal/servers     validation, settings, lifecycle with quota checks
internal/versions    cached Mojang version list
web                  Svelte + Vite + Tailwind UI, embedded with go:embed
charts/kubecraft     Helm chart
hack/e2e.sh          end-to-end test
```

## Not built yet

Backups, a world file browser, full modpacks (CurseForge/Modrinth `.mrpack`), OIDC login, and mc-router scale-from-zero.
