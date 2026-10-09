# Reconciler

Code: `internal/reconcile`, `internal/k8s/observe.go`, `internal/servers/manager.go`.

Runs every 5 seconds. One pass:

1. **Snapshot**: list StatefulSets, Pods, Services, Secrets and PVCs with `app.kubernetes.io/managed-by=mc-manager` (one call per kind), grouped by server id. Helper pods are tracked apart.
2. **Load** all servers and plans from Postgres.
3. **Per server**:
   - `deleting`: remove cluster resources, then the row.
   - Otherwise check drift: a missing resource, a different spec hash, or wrong replicas (not checked for sleeping servers). On drift, call `Ensure`.
   - Compute the state (below), keep it in memory for the API, and store it in `servers.observed_state` when it changed.
   - Remove the helper pod if it finished or the server pod exists.
4. **Orphans**: resources whose server id is not in the database lose their StatefulSet, Service and Secret. The PVC is kept as retained.

## States

| State | When |
|---|---|
| `creating` | No StatefulSet yet |
| `starting` | Pod missing, pending, or not ready yet |
| `running` | The `minecraft` container is ready |
| `stopping` | Desired stopped but a pod still exists, or the pod is terminating (also during restart) |
| `stopped` | Desired stopped and no pod |
| `sleeping` | Auto sleep, desired running, replicas 0 set by mc-router |
| `crashloop` | `CrashLoopBackOff`; an OOM kill gets a "try a bigger plan" message |
| `error` | Image pull failures, config errors, failed pods, or a failed `Ensure` |
| `deleting` | Marked for deletion |

Only the `minecraft` container decides the state, so the file sidecar being ready never makes a booting server look running.

## Failure handling

- A failed Kubernetes call during create or update marks the server `error` with the message. The next pass retries.
- Errors of one server do not stop the pass for others.
- Metrics: `kubecraft_reconcile_runs_total`, `kubecraft_reconcile_duration_seconds`, `kubecraft_reconcile_apply_errors_total`.
