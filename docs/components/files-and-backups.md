# Files and backups

Code: `internal/files` (sidecar), `internal/api/handlers_files.go`, `FilesAddr` in `internal/k8s/client.go`.

## Why a sidecar

Reading files from a pod is usually done with `kubectl exec`. That needs `pods/exec`, which allows running any command in any pod of the namespace. Instead, every server pod runs a second container, `files`, from the manager image, with `/data` shared. It offers four calls and nothing else.

## Sidecar API

Listens on 8081, every call needs `Authorization: Bearer <files-token>` (from the server's Secret). NetworkPolicy allows the port only from the manager.

| Call | Does |
|---|---|
| `GET /list?path=` | Directory entries with size, time, and whether editable |
| `GET /read?path=` | Content of an editable file |
| `PUT /write?path=` | Replace an existing editable file (temp file and rename) |
| `GET /backup?scope=world\|full` | Stream a tar.gz |

All paths go through Go's `os.Root` opened on `/data`, so `..`, absolute paths and symlinks cannot reach outside. Editable means: an existing regular file, at most 1 MiB, UTF-8, with an extension from `.yml .yaml .json .json5 .toml .properties .conf .cfg .txt .ini .snbt .mcmeta .csv .lang`. New files cannot be created and jars cannot be changed.

## Backups

- **world**: every top-level folder with a `level.dat` (world, nether, end), plus `whitelist.json`, `ops.json`, `banned-players.json`, `server.properties`.
- **full**: all of `/data` except `libraries`, `versions`, `cache`, `logs`, `crash-reports`, `bundler` and top-level jars (downloaded again on start).
- `session.lock` and symlinks are skipped.

For a running server the manager first sends `save-off` and `save-all flush` over RCON, streams the archive, then sends `save-on` (also when the download is cut). The file name is `<slug>-<scope>-<UTC time>.tar.gz`. Every download is audited.

## Stopped and sleeping servers

No server pod means no sidecar. The manager then creates the helper pod `mc-<slug>-files` with the same volume and only the `files` container, and waits until it runs (up to 90 seconds). The helper:

- ends by itself after 30 minutes (`activeDeadlineSeconds`),
- is deleted when the server starts (start action, or the reconciler as soon as a server pod exists),
- is deleted with the server.

With ReadWriteOnce storage on another node, a server that wakes while the helper runs waits a few seconds for the volume to move.

## Turning it off

`files.enabled: false` removes the sidecar and empties `FILES_IMAGE`; the UI hides backups and the API answers that file access is disabled.
