# Quotas and plans

Code: `internal/quota`, `servers.Limits` and the lifecycle methods in `internal/servers/manager.go`.

## Plans

Admin-managed sizes: CPU request and limit (millicores), memory limit (MB), disk (GB), active flag. Users pick a plan and never type numbers. Three plans are seeded on first start: Small (1 CPU, 2 GB, 5 GB), Medium (2 CPU, 4 GB, 10 GB), Large (4 CPU, 8 GB, 20 GB).

- Inactive plans are hidden and cannot be chosen; servers on them keep running.
- A plan in use cannot be deleted, and its disk cannot shrink.
- Editing a plan changes the template of its servers; the new limits apply on their next restart.

## Limits

Effective limit = user override if set, else the default quota, else unlimited.

| Limit | Counts |
|---|---|
| `max_servers` | All servers of the user |
| `max_total_storage_gb` | Disk of all servers |
| `max_total_cpu_m` | CPU limit of servers with desired state running (sleeping included) |
| `max_total_memory_mb` | Memory limit of the same servers |

## When checks run

| Action | Checks |
|---|---|
| Create | servers +1, disk, CPU and memory (new servers start right away) |
| Start a stopped server | CPU and memory |
| Plan change by the owner | the full new footprint against usage without this server |
| Plan change by an admin | none (admins can override) |
| Stop, delete, plan downgrade | none |

## No races

Each check runs in a transaction that first locks the owner's `users` row (`SELECT ... FOR UPDATE`), then reads usage, checks, and writes. Two parallel creates of the same user run one after the other, so a quota of 1 can never end with 2 servers. A test sends 8 parallel creates to prove it.

## Backstop

The chart adds a ResourceQuota and LimitRange to the namespace (values `resourceQuota` and `limitRange`), so a bug in the manager still cannot use more than the namespace allows. Server pods count their sidecar too (32 MiB request).

Errors read like `Quota exceeded: 3/3 servers` and are returned as HTTP 409.
