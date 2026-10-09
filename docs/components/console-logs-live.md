# Console, logs and live data

Code: `internal/api/ws.go`, `internal/live`, `internal/k8s/logs.go`.

## Logs socket

`GET /api/servers/{id}/logs` upgrades to a WebSocket and streams the `minecraft` container log:

- First attach sends the last 200 lines, then follows.
- When the stream ends (restart, stop), it waits and re-attaches with `sinceTime`. Lines carry timestamps, so lines already sent are skipped.
- While no pod exists, status messages say "server is not running" or "waiting for the server to start".
- Messages are buffered (256). A client that cannot take a message within 10 seconds is dropped, so a slow browser never blocks the log stream.

## Console socket

`GET /api/servers/{id}/console` is the same stream plus commands. The browser sends `{"type":"command","command":"..."}`; the manager runs it over RCON and sends the output back. Rules:

- One line, at most 1000 characters, at most one command every 100 ms.
- Every command goes into the audit log.
- Users with the console disabled by an admin get 403 and can still use the logs socket.
- If RCON is not reachable yet (server starting), the reply is an error message and the UI keeps the input disabled until the server runs.

## RCON pool

One connection per server, shared by the console, the poller and settings updates, with commands serialized per server. Address `mc-<slug>.<namespace>.svc:25575`, password read from the Secret on each new connection. A broken connection is replaced once before reporting "unavailable". Minecraft color codes are stripped from output. NetworkPolicy allows RCON only from the manager.

## Live poller

Every 10 seconds, for servers in state `running`:

- **Players**: runs `list` and parses "There are N of a max of M players online: a, b" (and the `N/M` variant of some forks).
- **CPU and RAM**: one list call to `metrics.k8s.io` for all server pods. Without metrics-server these are simply missing.
- **World settings**: once per new pod (identified by its start time), runs `difficulty`, `defaultgamemode`, `whitelist on/off` and `gamerule pvp`. Unknown commands on old versions are ignored.

The API adds these numbers to server responses; the dashboard and the metrics endpoint read them from memory.

## Settings applied live

Saving settings on a running server also runs the world commands above when they changed. The Players tab applies `whitelist add/remove`, `op/deop` and `kick` immediately. These settings are excluded from the restart hash, so they never ask for a restart.
