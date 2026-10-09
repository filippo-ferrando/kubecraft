# Database

Code: `internal/db` (migrations, queries, generated code in `internal/db/sqlc`).

kubecraft needs an existing PostgreSQL 14+ database and a login for it. It never deploys or embeds a database.

## Connection

Two options in the chart:

- `database.host` (+ `port`, `name`, `sslmode`) with the user and password from `database.credentialsSecret`. The manager gets `PGHOST`, `PGPORT`, `PGDATABASE`, `PGSSLMODE`, `PGUSER`, `PGPASSWORD`. This suits CloudNativePG role secrets, which have `username` and `password` keys.
- `database.urlSecret`: a Secret key with a full `postgres://` URL.

The role needs to own the database (migrations create tables). It needs no superuser rights.

## Tables

| Table | Holds |
|---|---|
| `users` | username, argon2id hash, role, disabled, must change password, console enabled |
| `sessions` | HMAC of the token, user, CSRF token, expiry, IP, user agent |
| `plans` | name, CPU request and limit, memory, disk, active |
| `default_quota` | one row: global limits, NULL means unlimited |
| `user_quotas` | per-user overrides, NULL means "use the default" |
| `servers` | owner, slug (unique), plan, type, version, settings (jsonb), desired state, keep data, observed state |
| `audit_log` | user, action, target, details (jsonb), time |

Foreign keys: deleting a user is refused while they own servers; a plan in use cannot be deleted (it can be deactivated).

## Migrations and seed

Migrations are SQL files embedded in the binary and applied with goose on every start. The seed adds the three default plans (Small, Medium, Large) if the plans table is empty, and the default quota row with `DEFAULT_MAX_SERVERS`.

## Queries

All SQL lives in `internal/db/queries/*.sql`; `make sqlc` regenerates Go code. Everything is parameterized. Transactions go through `Store.InTx`. Quota checks lock the owner's row (`SELECT ... FOR UPDATE`), see [quotas](quotas-and-plans.md).

## Backups of the database

Not handled by kubecraft. With CloudNativePG, the cluster's own backups cover it. The database only holds metadata; worlds live on the server volumes.
