# Project: Minecraft Server Manager for Kubernetes (k3s)

Build a multi-user web application, written in Go, that lets users create and manage their own Minecraft servers. Each server runs as its own workload in a k3s cluster. Deployment is GitOps via ArgoCD.

Read the whole document first. Then propose a short plan and build it phase by phase (see "Phases"). Ask me before deviating from anything marked **MUST**.

---

## 1. Goals

- Management UI at `https://mine.fferrando.cc`.
- Every Minecraft server is reachable at `<slug>.mine.fferrando.cc` on the default port **25565**, with no per-server NodePort or LoadBalancer.
- Two roles: **admin** and **user**. Users only see and control their own servers.
- Server creation is limited by per-user quotas. Each server's resources are capped by an admin-defined plan.
- Live logs and an interactive console in the browser.
- Simple, fast, nice-looking UI.

## 2. Tech stack

| Part | Choice |
|---|---|
| Backend | **Go** (1.22+), `chi` router, `client-go`, `pgx`, `golang-migrate` (or `goose`), `sqlc` for queries |
| Database | **PostgreSQL** (connection via `DATABASE_URL` from a Secret; assume Postgres is provided externally or by CloudNativePG, do not deploy a DB in this project unless I ask) |
| Frontend | Lightweight SPA: **Svelte + Vite + Tailwind** (no heavy UI frameworks), **xterm.js** for the console. Built to static files and embedded in the Go binary with `go:embed`, so the whole app is **one container image** |
| Realtime | WebSocket (`coder/websocket` or `gorilla/websocket`) for logs and console |
| RCON | `github.com/gorcon/rcon` or equivalent |
| Server image | `itzg/minecraft-server` |
| Routing | `itzg/mc-router` (hostname-based routing on 25565) |
| Cluster | k3s (Traefik ingress, metrics-server, servicelb all included by default) |
| Packaging | Dockerfile (multi-stage), plain Kubernetes manifests in `deploy/` (or Kustomize) that ArgoCD can sync |

## 3. Architecture

```
Browser ──HTTPS──> Traefik Ingress (mine.fferrando.cc) ──> Manager (Go: API + static UI)
                                                              │         │
                                                              │         └──> PostgreSQL (users, plans, quotas, servers)
                                                              └──> Kubernetes API (ServiceAccount, namespaced Role)
                                                                     │
Minecraft client ──TCP 25565──> mc-router ──(by hostname)──> Service/Pod per server
   (*.mine.fferrando.cc)
```

- **MUST:** only the backend talks to the Kubernetes API, using its in-cluster ServiceAccount. Never use an admin kubeconfig, and the browser never talks to the cluster.
- **MUST:** authorization is enforced in the backend on every request. The UI hiding a button is not security.
- Postgres is the source of truth for ownership, plans, and quotas. Kubernetes is the source of truth for runtime state. Reconcile the two (see section 8).

## 4. Domains, DNS, TLS

- `mine.fferrando.cc` → the manager UI/API, via a Traefik `Ingress` (or `IngressRoute`) with TLS (cert-manager, assume a ClusterIssuer exists, make its name configurable).
- `*.mine.fferrando.cc` → wildcard DNS record pointing at the node/LB IP that exposes mc-router on **TCP 25565**.
- If the DNS provider is Cloudflare, the wildcard record must be **DNS-only (not proxied)**, since Minecraft traffic is raw TCP. Note this in the README.
- mc-router is exposed with a `Service` of type `LoadBalancer` (k3s servicelb) on port 25565. Make the type configurable.
- The UI domain and the server base domain are configuration (`UI_HOST=mine.fferrando.cc`, `SERVER_BASE_DOMAIN=mine.fferrando.cc`), not hardcoded.
- The server slug must be a valid DNS label (`^[a-z0-9]([-a-z0-9]{0,30}[a-z0-9])?$`), unique, and not in a reserved list (`www`, `api`, `admin`, `mc-router`, etc.).

## 5. Users and auth

- Local accounts. Passwords hashed with **argon2id**. Server-side sessions stored in Postgres, delivered as `HttpOnly`, `Secure`, `SameSite=Lax` cookies. CSRF protection on state-changing requests.
- Rate-limit login attempts.
- Roles: `admin`, `user`.
- Bootstrap: if no users exist, create an admin from env vars `BOOTSTRAP_ADMIN_USER` / `BOOTSTRAP_ADMIN_PASSWORD` (from a Secret), and force a password change on first login.
- No public self-registration by default. Admin creates users. Optional later: invite links.
- Design the auth layer behind an interface so OIDC can be added later without refactoring.
- Admin can: create/disable/delete users, reset passwords, set per-user quotas, manage plans, see and manage all servers.
- User can: create, view, control, and delete **only their own** servers, and change their own password.
- Every server API route checks `server.owner_id == current_user.id OR current_user.role == admin`.

## 6. Plans and limits

**Plans** (admin-managed, stored in Postgres):

| Field | Example |
|---|---|
| name | Small |
| cpu_request / cpu_limit | 500m / 1000m |
| memory_limit_mb | 2048 |
| storage_gb | 5 |
| active | true |

Seed three plans on first start (Small 1 CPU/2 GB/5 GB, Medium 2 CPU/4 GB/10 GB, Large 4 CPU/8 GB/20 GB). Users pick a plan and never type raw numbers.

**Per-user quota** (admin-editable, with a global default):

- `max_servers` (total servers owned)
- `max_total_cpu` and `max_total_memory_mb` across the user's **running** servers (optional, `NULL` = unlimited)
- `max_total_storage_gb` (optional)

Rules:

- Checked in the backend inside a DB transaction when creating a server or starting a stopped one. Use row locking on the user (or an advisory lock) to avoid races that exceed quota.
- Return clear errors ("Quota exceeded: 3/3 servers").
- Plan changes on an existing server are allowed (admin only, or user if quota allows) and require a restart.

**Per-pod cap:**

- Container `requests`/`limits` come from the plan.
- **MUST:** set the JVM heap via the `MEMORY` env var to about **75% of the memory limit** (leave headroom for metaspace, threads, and native memory), otherwise the pod is OOMKilled. Make the ratio a config value.

**Cluster backstop:**

- Ship a `ResourceQuota` (and `LimitRange` with sane defaults) in the servers namespace to cap totals even if the backend has a bug. Values are configurable in the manifests.

## 7. Kubernetes resources per server

Namespace: `minecraft` (configurable). The manager, mc-router, and all servers live there to keep RBAC simple. Name resources `mc-<slug>`.

Per server, the backend creates:

1. **Secret** `mc-<slug>-rcon`: generated RCON password (never shown in the UI or API responses).
2. **PersistentVolumeClaim** `mc-<slug>-data`: size from the plan, default storage class (configurable).
3. **StatefulSet** `mc-<slug>` with `replicas: 1` (0 when stopped):
   - Image `itzg/minecraft-server:<tag>` (tag configurable, default `latest`; allow the admin to pin it).
   - Env: `EULA=TRUE` (the UI must show an EULA checkbox the user accepts at creation), `TYPE`, `VERSION`, `MEMORY`, `ENABLE_RCON=true`, `RCON_PASSWORD` (from the Secret), plus user-configurable options (see below).
   - Resources from the plan.
   - Readiness/startup probe using `mc-health` (shipped in the itzg image), with a generous startup timeout.
   - `terminationGracePeriodSeconds` of about 60, so the world saves on stop.
   - Mount the PVC at `/data`.
   - Pod security: `runAsNonRoot` where the image allows it, drop capabilities, no privileged, no host mounts.
4. **Service** (ClusterIP) `mc-<slug>` exposing 25565 (and 25575 RCON **only inside the cluster**, never exposed externally). Annotation for mc-router: `mc-router.itzg.me/externalServerName: <slug>.<SERVER_BASE_DOMAIN>`.

Labels on everything: `app.kubernetes.io/managed-by=mc-manager`, `mc-manager/server-id=<uuid>`, `mc-manager/owner-id=<uuid>`, `mc-manager/slug=<slug>`.

**Supported server types in the wizard (phase 1):** Vanilla, Paper, Purpur, Fabric, Forge, NeoForge. Versions: `LATEST` or a specific version, with a text input and a dropdown of known versions (fetch the list from Mojang's version manifest, cached). Modpacks (CurseForge/Modrinth) are a later phase.

**User-configurable settings (whitelisted only):** MOTD, difficulty, gamemode, max players, view distance, whitelist on/off + whitelist entries, ops, PVP, seed, level type, online-mode. Anything else is rejected. Do not let users inject arbitrary env vars, volumes, or images.

**mc-router:**

- Deployed via manifests in `deploy/`, running with `-in-kube-cluster` so it watches Services in the namespace and discovers hostnames from the annotation.
- Own ServiceAccount + Role with get/list/watch on services (and endpoints if required), nothing else.
- Exposed on 25565 (see section 4).
- Optional later: mc-router's auto-scale-up/down so stopped servers wake on connection.

**Lifecycle actions:** create, start (scale to 1), stop (scale to 0, graceful), restart (delete pod), delete (with confirmation; option to keep or delete the PVC/world; default is delete everything after typing the server name).

## 8. Backend design

**Postgres schema (minimum):**

- `users` (id, username unique, password_hash, role, disabled, must_change_password, created_at)
- `sessions` (id, user_id, expires_at, created_at, ip, user_agent)
- `plans` (id, name, cpu_request_m, cpu_limit_m, memory_limit_mb, storage_gb, active)
- `user_quotas` (user_id PK, max_servers, max_total_cpu_m, max_total_memory_mb, max_total_storage_gb; NULL = use default)
- `servers` (id, owner_id, slug unique, display_name, plan_id, type, version, settings jsonb, desired_state `running|stopped`, created_at)
- `audit_log` (id, user_id, action, target, details jsonb, created_at)

**Reconciliation:**

- A background reconciler (and/or informers via `client-go`) keeps DB and cluster aligned: detect servers whose K8s resources are missing or drifted, and report observed status (`creating`, `starting`, `running`, `stopping`, `stopped`, `error`, `crashloop`) from pod/StatefulSet state.
- Creation is idempotent and rolls back (or marks `error` with a retry option) if a K8s call fails partway.

**API (JSON, prefix `/api`):**

- `POST /auth/login`, `POST /auth/logout`, `GET /me`, `POST /me/password`
- `GET/POST /servers`, `GET/PATCH/DELETE /servers/{id}`
- `POST /servers/{id}/start|stop|restart`
- `GET /servers/{id}/status` (state, players online, version, uptime, CPU/RAM usage)
- `GET /servers/{id}/logs` (WebSocket: backlog of the last N lines, then follow)
- `GET /servers/{id}/console` (WebSocket: send command → RCON, receive output)
- `GET /plans` (active ones, for the wizard)
- `GET /me/quota` (used vs limit)
- Admin: `/admin/users`, `/admin/plans`, `/admin/servers`, `/admin/quotas`, `/admin/audit`
- Health: `/healthz`, `/readyz`. Metrics: `/metrics` (Prometheus), optional.

**Logs:** stream `pods/log` (follow, timestamps optional) from the server pod over WebSocket, with backpressure and cleanup on disconnect. Handle pod restarts by re-attaching.

**Console:**

- Backend opens RCON to `mc-<slug>.<ns>.svc:25575` using the password from the Secret, and relays commands and responses over the WebSocket.
- Show logs and the command input together in one terminal view (xterm.js), so output from commands appears with the live log.
- Allow-list/deny-list is not required, but admins can disable the console per user (flag), and every command is written to `audit_log`.
- Handle RCON being unavailable while the server is starting (clear UI state, auto-retry).

**Status and metrics:**

- Players online/max via RCON `list` or the server-list ping, polled with a short cache.
- CPU/RAM from `metrics.k8s.io` (metrics-server).

**Security notes:**

- Input validation on everything: slug, version, settings whitelist, WebSocket origin check.
- Parameterised queries only (sqlc).
- Secrets never logged or returned by the API.
- The manager's Role is **namespaced** (not ClusterRole), limited to: `statefulsets`, `services`, `persistentvolumeclaims`, `secrets`, `configmaps`, `pods`, `pods/log`, `events` (read), and `pods.metrics.k8s.io` (read). No access to anything else, and **no cluster-admin**.

## 9. Frontend

Goal: simple, fast, and tidy. Dark mode by default with a light toggle. Responsive (usable on mobile).

Pages:

- **Login**
- **Dashboard:** server cards with status badge, players online, CPU/RAM bars, quick start/stop, and the connect address (`slug.mine.fferrando.cc`) with a copy button. Shows the quota usage ("2/3 servers, 3/6 GB RAM").
- **Create wizard:** name/slug (live availability + validation) → type → version → plan (shows what the user's remaining quota allows) → settings → EULA checkbox → create.
- **Server page** (tabs):
  - *Console* (live log + command input, xterm.js)
  - *Overview* (status, players, resources, address, start/stop/restart)
  - *Settings* (whitelisted properties, with a restart-required hint)
  - *Players* (online, whitelist, ops)
  - *Backups* (placeholder until phase 4)
  - *Danger zone* (delete)
- **Account:** change password.
- **Admin:** Users (CRUD, quotas, disable), Plans (CRUD), All servers (filter by owner, force stop/delete), Audit log.

Live status via polling every few seconds or Server-Sent Events/WebSocket. Keep the bundle small.

## 10. Repository layout

```
.
├── cmd/manager/main.go
├── internal/
│   ├── api/            # HTTP handlers, middleware, WS
│   ├── auth/           # sessions, argon2, interface for future OIDC
│   ├── db/             # sqlc output, migrations/
│   ├── k8s/            # client-go wrappers, server resource builders
│   ├── quota/          # quota/plan logic (with unit tests)
│   ├── rcon/
│   ├── reconcile/
│   └── config/
├── web/                # Svelte + Vite + Tailwind app (built and embedded)
├── deploy/             # manifests for ArgoCD
│   ├── namespace.yaml
│   ├── manager/        # Deployment, Service, Ingress, ServiceAccount, Role, RoleBinding
│   ├── mc-router/      # Deployment, Service (25565), ServiceAccount, Role, RoleBinding
│   ├── resourcequota.yaml
│   └── kustomization.yaml
├── Dockerfile
├── Makefile
└── README.md           # setup, DNS notes, ArgoCD Application example, config reference
```

## 11. Configuration (env vars)

`DATABASE_URL`, `UI_HOST`, `SERVER_BASE_DOMAIN`, `K8S_NAMESPACE`, `STORAGE_CLASS`, `MC_IMAGE_TAG`, `JVM_HEAP_RATIO` (default 0.75), `SESSION_SECRET`, `BOOTSTRAP_ADMIN_USER`, `BOOTSTRAP_ADMIN_PASSWORD`, `DEFAULT_MAX_SERVERS`, `COOKIE_SECURE`, `LOG_LEVEL`.

## 12. Phases

1. **Foundation:** repo, DB migrations, auth (login, sessions, bootstrap admin), config, Dockerfile, base manifests with RBAC, Ingress, and mc-router. Health endpoints.
2. **Core servers:** plans, quotas, create/start/stop/restart/delete, K8s resource builders, reconciler, dashboard + create wizard + server overview.
3. **Console & logs:** log streaming, RCON console, status/metrics, players tab.
4. **Admin & polish:** admin UI (users, plans, quotas, all servers, audit log), settings editor, error handling, empty states.
5. **Later (do not build now):** backups (CronJob → S3/NFS, restore), file browser for worlds/mods, modpacks (CurseForge/Modrinth), OIDC, mc-router auto-scale-from-zero, optional CRD + controller.

## 13. Quality bar

- Unit tests for quota logic, slug validation, resource builders, and authorization checks. Integration tests for the API against a test Postgres, and the K8s layer tested with `client-go` fake clientset (or envtest).
- `golangci-lint` clean, `make` targets for build/test/lint/run/docker.
- Structured logging (`slog`), graceful shutdown, context propagation everywhere.
- README explains how to deploy through ArgoCD (include an example `Application`), DNS setup for `mine.fferrando.cc` and `*.mine.fferrando.cc`, and how to create the required Secrets.

## 14. Acceptance criteria (phase 1-3)

- An admin can log in, create a user with a quota of 2 servers, and the user can create a Paper server from the wizard.
- Within a couple of minutes, a Minecraft client can connect to `<slug>.mine.fferrando.cc:25565`.
- The user cannot create a third server, and cannot see or access another user's servers (verified by API tests).
- Pod memory/CPU limits match the plan, and the JVM heap is ~75% of the memory limit.
- Live logs stream in the browser. Commands typed in the console execute via RCON and show their output.
- Stopping a server scales it to 0 and keeps the world, and starting it again restores it.
- The manager runs with a namespaced Role only.

## 15. Questions to ask me before starting if unclear

- Name of the cert-manager ClusterIssuer and the storage class to use.
- How Postgres is provided (existing instance vs CloudNativePG) and the Secret name for `DATABASE_URL`.
- Container registry and image name to push the manager image to.
