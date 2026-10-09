# Manager and API

Code: `cmd/manager`, `internal/api`, `internal/auth`, `internal/config`.

The manager is one process: HTTP API, embedded UI, reconciler, live poller and metrics. On start it loads the config from env, connects to Postgres (retrying for about a minute), runs migrations, seeds the default plans and quota, creates the bootstrap admin if no user exists, and starts the loops.

## Routes

All JSON routes live under `/api`. Health checks are outside it.

| Route | Who | Purpose |
|---|---|---|
| `GET /healthz`, `GET /readyz` | anyone | Liveness, and readiness (database and Kubernetes reachable) |
| `POST /api/auth/login`, `POST /api/auth/logout` | anyone / logged in | Session cookie |
| `GET /api/me`, `POST /api/me/password` | logged in | Current user and CSRF token, own password |
| `GET /api/me/quota` | user | Limits and usage |
| `GET /api/plans`, `/api/meta`, `/api/versions` | user | Wizard data |
| `GET /api/catalog/search`, `/api/catalog/projects` | user | Modrinth search proxy |
| `GET,POST /api/servers` | user | Own servers, create |
| `GET /api/servers/slug-available` | user | Live name check |
| `GET,PATCH,DELETE /api/servers/{id}` | owner or admin | Details, settings and plan, delete |
| `POST /api/servers/{id}/start,stop,restart` | owner or admin | Lifecycle |
| `POST /api/servers/{id}/players` | owner or admin | Whitelist, ops, kick |
| `GET /api/servers/{id}/logs` | owner or admin | WebSocket, live log |
| `GET /api/servers/{id}/console` | owner or admin, console enabled | WebSocket, log plus RCON commands |
| `GET /api/servers/{id}/files`, `GET,PUT .../files/content` | owner or admin | File browser and editor |
| `GET /api/servers/{id}/backup?scope=world\|full` | owner or admin | tar.gz download |
| `/api/admin/users`, `/plans`, `/quotas`, `/servers`, `/audit` | admin | Administration |

Users with `must_change_password` can only reach `/me`, `/me/password` and logout until they change it.

## Authentication

- Local accounts, passwords hashed with argon2id (19 MiB, 2 iterations). Minimum length 10.
- `Authenticator` is an interface, so an OIDC provider can be added next to the local one later.
- Login is rate limited: 5 failures per user and 20 per IP within 15 minutes. Behind the ingress, the client IP comes from the last `X-Forwarded-For` entry, but only when the direct peer is a private address.
- Sessions: a random 32-byte token in an `HttpOnly`, `SameSite=Lax` (and `Secure`) cookie. The database stores an HMAC of the token with `SESSION_SECRET`, so a copy of the table cannot be used to log in.
- Changing the own password ends other sessions. An admin reset ends all sessions and forces a change at next login. Disabling a user ends all sessions at once.
- Bootstrap: when no user exists, `BOOTSTRAP_ADMIN_USER` / `BOOTSTRAP_ADMIN_PASSWORD` create an admin, under an advisory lock so two starts cannot both create one.

## CSRF and origins

State-changing requests must:

1. come from an allowed origin (`https://UI_HOST` plus `ALLOWED_ORIGINS`), checked on `Origin` or `Referer`, and
2. carry the session's CSRF token in `X-CSRF-Token`. The UI gets it from `/api/me` and login.

WebSockets use the same origin list.

## Authorization

- Every server route loads the server and allows it only for the owner or an admin. Others get 404.
- Admin routes check the role.
- Admin guards: no self-demote, self-disable or self-delete; the last active admin cannot be removed; users that still own servers cannot be deleted.

## Validation

Input is strict JSON (unknown fields rejected, 1 MiB limit). Slugs, versions, types, display names, settings and player names are checked in `internal/servers`. Settings are a fixed struct, so no env var, image or volume can be passed through.

## Audit log

Logins, failed logins, server actions, console commands, player changes, file writes, backups and admin changes are written to `audit_log` with the user, target and details (including the client IP). Admins browse it in the UI.

## Configuration

See the table in the main README. Notable: `SESSION_SECRET` is required, `METRICS_ADDR` set to an empty value disables metrics, `FILES_IMAGE` empty disables files and backups, `AUTO_SLEEP` tells the UI whether mc-router can scale servers.
