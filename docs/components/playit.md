# playit.gg

Code: `internal/playit` (reads tunnels), `internal/gate` (the gateway), `PlayitPort` in `internal/k8s/builders.go`, chart template `playit-gateway.yaml`.

playit.gg relays TCP from the internet to an agent that dials out, so servers can be published behind CGNAT without a public IP or a VPS. It is optional: without it a server's address is `<slug>.<serverBaseDomain>`, and making that name public later (a VPS, a public DNS record, a Traefik TCP route to mc-router) needs no change in kubecraft.

## Flow

```
friend -> <name>.tun.ply.gg (playit) -> playit agent in the gateway pod
       -> 127.0.0.1:<tunnel local port> -> gate -> mc-router:25565 as <slug>.<serverBaseDomain>
       -> Service mc-<slug> -> server pod
```

1. A server is created as usual. Its address is `<slug>.<serverBaseDomain>` and the server page shows how to create its tunnel.
2. In the playit dashboard, the owner (or an admin) adds a tunnel: type **Minecraft Java**, assigned to the kubecraft agent, name **`kubecraft-<slug>`**, local address **`127.0.0.1`**, a free local port (the UI suggests one from `playit.firstPort` up).
3. The manager lists the agent's tunnels every 30 seconds. When `kubecraft-<slug>` appears, its playit address becomes the server's `address` in the API and UI (`private_address` keeps the LAN/Tailscale name), and the reconciler puts the tunnel's local port on the server Service (`mc-manager/playit-port`).
4. The gate, next to the agent, watches those Services and listens on each port. A connection on a port is always sent to that port's server: the handshake is rewritten to the server's private name and handed to mc-router.

Deleting the tunnel in the dashboard reverts the server to its private address within a minute and closes the port.

## Why tunnels are created by hand

The agent key that the playit agent runs with is read only for account actions: creating tunnels with it fails with `NotAllowedWithReadOnly`. Listing works, so kubecraft discovers tunnels by name instead of creating them. playit also wants the agent online when a tunnel is created; in the cluster it always is.

## Why routing by port, not by hostname

- A client connecting through playit sends a playit hostname (or whatever it wants) in its handshake. Routing on that would let anyone reach any server, including private ones, by forging a name. With one port per tunnel, a tunnel can only ever reach its own server.
- Going through mc-router keeps auto sleep working: mc-router sees the connections, so it does not put a server to sleep while friends play, and a join through playit wakes a sleeping server.
- mc-router's own names never change, which avoids a mc-router quirk where removing a hostname cancels the server's idle timer.

## Checks

A tunnel is routed only when it is `Routable`: local address `127.0.0.1`, local port 1024 to 65535, and no other `kubecraft-` tunnel with the same port or the same name. Other problems (disabled in the dashboard, over the plan limit, no address yet) are shown on the server page and returned in `playit.problems`. If listing tunnels fails, the last good state is kept and admins see the error.

## Pods and permissions

The gateway Deployment (`<release>-playit-gateway`, one replica, `Recreate` so two agents never share a key) has two containers:

- `gate`: the manager image started as `manager gate`, UID 65532, read-only root, listens on `127.0.0.1` only. Its ServiceAccount can only read Services.
- `playit`: `ghcr.io/playit-cloud/playit-agent`, UID 1000, read-only root, socket in an `emptyDir` at `/tmp`.

A NetworkPolicy denies all inbound traffic to the pod: the agent dials out, and the gate is reached over localhost.

The manager reads the same agent key (`PLAYIT_SECRET`) to list tunnels; metric `kubecraft_playit_syncs_total{result}`.

## Limits

- One tunnel per server; free playit plans limit the number of tunnels and may require the agent to stay within plan limits. Problems show on the server page.
- Java Edition only.
- The relay sees connection metadata; in online mode Minecraft encrypts the session after login. Whitelists still apply as usual.
