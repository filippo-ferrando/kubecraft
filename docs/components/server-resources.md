# Server resources

Code: `internal/k8s/builders.go`, `internal/k8s/client.go`.

Each server is a set of resources named after its slug, all in the release namespace and all labelled:

```
app.kubernetes.io/managed-by: mc-manager
mc-manager/server-id: <uuid>
mc-manager/owner-id: <uuid>
mc-manager/slug: <slug>
```

## Secret `mc-<slug>-rcon`

Two random values: `password` (RCON) and `files-token` (file sidecar). Created once and never rotated by kubecraft. Never returned by the API or written to logs.

## PersistentVolumeClaim `mc-<slug>-data`

ReadWriteOnce, size from the plan, storage class from `manager.storageClass` (empty uses the cluster default). It only grows: a plan with less disk is refused, a bigger one resizes the claim (the storage class needs `allowVolumeExpansion`).

## Service `mc-<slug>`

ClusterIP with port `minecraft` (25565) and `rcon` (25575). Annotations for mc-router:

| Annotation | Value |
|---|---|
| `mc-router.itzg.me/externalServerName` | `<slug>.<serverBaseDomain>` |
| `mc-router.itzg.me/autoScaleUp` / `autoScaleDown` | `true` only for running servers with auto sleep, else `false` |
| `mc-router.itzg.me/autoScaleAsleepMOTD` / `autoScaleLoadingMOTD` | Texts shown while asleep or waking |

The auto scale annotations are only set when mc-router runs with auto scale.

## StatefulSet `mc-<slug>`

- `replicas` 1 when running, 0 when stopped. For sleeping servers mc-router owns the replica count.
- `updateStrategy: OnDelete`: template changes wait for a restart.
- Annotations: `mc-manager/spec-hash` (whole template, used to detect drift) and, on the pod template, `mc-manager/restart-hash` (without settings applied live). A running pod with an older restart hash means "restart required".

### Container `minecraft`

- Image `itzg/minecraft-server:<manager.minecraftImageTag>`.
- Env: `EULA=TRUE`, `TYPE`, `VERSION`, `MEMORY` (heap = memory limit x `JVM_HEAP_RATIO`), `ENABLE_RCON`, `RCON_PORT`, `RCON_PASSWORD` (from the Secret), `ENABLE_AUTOPAUSE=false`, `STOP_DURATION=50`, plus the user's settings (`MOTD`, `DIFFICULTY`, `MODE`, `MAX_PLAYERS`, `VIEW_DISTANCE`, `PVP`, `LEVEL_TYPE`, `ONLINE_MODE`, whitelist and ops with `SYNCHRONIZE`, `SEED`, `MODRINTH_PROJECTS`, `MODRINTH_DOWNLOAD_DEPENDENCIES=required`).
- Resources from the plan; memory request equals the limit.
- Probes with `mc-health`: startup (up to 20 minutes for first downloads), readiness, liveness.
- Security: UID/GID 1000, `fsGroup` 1000, non-root, no privilege escalation, all capabilities dropped, seccomp `RuntimeDefault`, no service account token, no service links (their env vars could clash with the image settings), no host mounts.
- `terminationGracePeriodSeconds: 60` so the world is saved on stop.

### Container `files`

The file sidecar, only when `files.enabled`. See [files and backups](files-and-backups.md). Small resources (10m/32Mi request, 500m/128Mi limit), read-only root filesystem, same UID.

## Helper pod `mc-<slug>-files`

Only while a stopped or sleeping server's files are used. Same volume, only the `files` container, `restartPolicy: Never`, `activeDeadlineSeconds: 1800`. It has no `server-id` label, so it is never mistaken for the server pod.

## Ensure logic

`Ensure` is called on create, on every change, and by the reconciler when something drifted. For each resource: create if missing; refuse with a conflict if a resource of that name belongs to another server or a retained world; otherwise update only what changed. It is safe to call any number of times.

## Delete logic

StatefulSet, Service, Secret and helper pod are removed. The PVC is deleted, or, with "keep world", relabelled with `mc-manager/retained-from=<slug>` and stripped of the server labels. A retained PVC blocks that slug until an admin deletes it with kubectl.
