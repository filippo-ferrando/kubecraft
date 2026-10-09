# playit.gg

Code: `internal/playit` (creates, lists and deletes tunnels), `internal/gate` (the gateway), `PlayitPort` in `internal/k8s/builders.go`, chart template `playit-gateway.yaml`.

playit.gg relays TCP from the internet to an agent that dials out, so servers can be published behind CGNAT without a public IP or a VPS. It is optional: without it a server's address is `<slug>.<serverBaseDomain>`, and making that name public later (a VPS, a public DNS record, a Traefik TCP route to mc-router) needs no change in kubecraft.

## Flow

```
friend -> <name>.tun.ply.gg (playit) -> playit agent in the gateway pod
       -> 127.0.0.1:<tunnel local port> -> gate -> mc-router:25565 as <slug>.<serverBaseDomain>
       -> Service mc-<slug> -> server pod
```

1. A server is marked **Public** (create wizard or settings; the switch only shows when playit is configured). Its address stays `<slug>.<serverBaseDomain>` until the tunnel is ready.
2. The reconciler (every 5 seconds) passes the set of public servers to the playit watcher. When the set changes, the watcher syncs right away; otherwise every 30 seconds, or every 5 seconds while a new tunnel waits for its address.
3. A sync lists the agent's tunnels and compares them with the public servers. A public server without a usable tunnel gets one: Minecraft Java, name `kubecraft-<slug>`, local address `127.0.0.1`, the first free local port from `playit.firstPort` up. Tunnels of servers that are no longer public (or deleted), extra tunnels of one server, and tunnels that cannot be routed are deleted.
4. When the tunnel has an address, it becomes the server's `address` in the API and UI (`private_address` keeps the LAN/Tailscale name), and the reconciler puts the tunnel's local port on the server Service (`mc-manager/playit-port`).
5. The gate, next to the agent, watches those Services and listens on each port. A connection on a port is always sent to that port's server: the handshake is rewritten to the server's private name and handed to mc-router.

Turning Public off removes the port from the Service at once and deletes the tunnel on the next sync. Stopping a server keeps its tunnel, so the address does not change.

## The agent key must be self-managed

playit has three kinds of agents. The key shown in the playit dashboard belongs to an *assignable* agent and is read only: creating tunnels with it fails with `NotAllowedWithReadOnly`. A *self-managed* agent may create and delete its own tunnels, which is what kubecraft needs. Self-managed agents are made with the claim flow, which `hack/playit-claim.sh` runs:

```bash
hack/playit-claim.sh minecraft kubecraft-playit | kubeseal --format yaml > playit_secret.yaml
```

It prints a `https://playit.gg/claim/<code>` link to open while logged in to playit.gg, then writes a Secret with the key (`SECRET_KEY`) on stdout. playit only accepts tunnels for an agent whose version it knows, so tunnels are created once the agent in the gateway pod has connected; until then the server page shows "the playit agent has not connected yet".

The watcher only looks at tunnels of its own agent. Tunnels of other agents, even if named `kubecraft-...`, are ignored and never deleted.

## Why routing by port, not by hostname

- A client connecting through playit sends a playit hostname (or whatever it wants) in its handshake. Routing on that would let anyone reach any server, including private ones, by forging a name. With one port per tunnel, a tunnel can only ever reach its own server.
- Going through mc-router keeps auto sleep working: mc-router sees the connections, so it does not put a server to sleep while friends play, and a join through playit wakes a sleeping server.
- mc-router's own names never change, which avoids a mc-router quirk where removing a hostname cancels the server's idle timer.

## Checks

A tunnel is routed only when it is `Routable`: local address `127.0.0.1`, local port 1024 to 65535, and no other `kubecraft-` tunnel with the same port or the same name. Kubecraft replaces tunnels that break these rules. Other problems (disabled in the dashboard, over the plan limit) and failed creates (for example `the playit.gg account has no free tunnels left`) are shown on the server page and returned in `playit.problems`. If a sync fails, the last good state is kept and admins see the error.

## Pods and permissions

The gateway Deployment (`<release>-playit-gateway`, one replica, `Recreate` so two agents never share a key) has two containers:

- `gate`: the manager image started as `manager gate`, UID 65532, read-only root, listens on `127.0.0.1` only. Its ServiceAccount can only read Services.
- `playit`: `ghcr.io/playit-cloud/playit-agent`, UID 1000, read-only root, socket in an `emptyDir` at `/tmp`.

A NetworkPolicy denies all inbound traffic to the pod: the agent dials out, and the gate is reached over localhost.

The manager uses the same agent key (`PLAYIT_SECRET`) to manage tunnels. Metrics: `kubecraft_playit_syncs_total{result}` and `kubecraft_playit_tunnel_actions_total{action,result}`.

## Limits

- One tunnel per public server; free playit plans limit the number of tunnels. When the limit is hit, the create error shows on the server page and is retried on every sync.
- Every owner may make their servers public.
- Java Edition only.
- The relay sees connection metadata; in online mode Minecraft encrypts the session after login. Whitelists still apply as usual.
