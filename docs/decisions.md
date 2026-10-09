# Design decisions

Each entry lists the choice, what else was considered, and why.

## Platform and packaging

**One Go binary with the UI embedded.** The Svelte app is built to static files and embedded with `go:embed`. One image, one Deployment, no separate web server. The same image also runs the file sidecar (`manager files`), so a release is a single image tag.

**Helm chart instead of plain manifests or Kustomize.** The chart needs real choices (Ingress or IngressRoute, database by URL or by host and secret, optional ServiceMonitor, auto sleep on or off). Helm templates handle that cleanly, and the chart is published as an OCI artifact next to the image, like other homelab apps.

**No database in the chart.** kubecraft only asks for Postgres login details. The cluster already runs a shared, backed-up CloudNativePG cluster, and a second database per app would duplicate backups and failover. The chart refuses to render without database settings.

**Postgres connection through `PG*` variables.** Building a URL from a username and password breaks when the password has characters like `/` or `@`. With the host in values and the user and password from a Secret, pgx reads `PGHOST`, `PGUSER`, `PGPASSWORD` and so on directly. A full URL in a Secret is still supported.

## Backend

**chi, pgx, sqlc, goose.** Small, standard pieces. sqlc generates typed Go from plain SQL, so every query is parameterized. goose runs embedded migrations on start. Only one manager runs at a time (single replica, `Recreate` strategy), so migrations never run twice in parallel.

**Polling instead of informers.** The reconciler lists StatefulSets, Pods, Services, Secrets and PVCs by label every 5 seconds. For tens of servers this is cheap, has no cache to warm up, and is easy to reason about. Informers can replace it if the number of servers grows a lot.

**Single replica.** The reconciler, poller and login rate limiter are per process. Running two replicas would need leader election and a shared rate limiter. A homelab does not need that, so the chart fixes `replicas: 1` with a `Recreate` strategy.

**Desired state in the database, observed state from the cluster.** Users change what they want; Kubernetes reports what is. Keeping both apart makes "stopped but pod still terminating" or "running but crashlooping" easy to show.

**`deleting` as a state.** Deleting in one go (cluster first, then row) can leave a half state if a call fails. Marking the row first lets the reconciler finish the job, and prevents it from recreating resources for a server that is going away.

**Orphans keep their volume.** Resources labelled as kubecraft's but without a database row are removed, except the PVC, which is kept as retained. If the database is ever restored from an older backup, worlds are not lost.

## Kubernetes resources

**StatefulSet per server with `replicas` 0 or 1.** Start and stop are a scale, the pod name is stable (`mc-<slug>-0`), and mc-router's auto scale works with StatefulSets.

**A separate PVC, not a volumeClaimTemplate.** The volume can grow on a plan change, and can be kept or deleted on server delete independently of the StatefulSet.

**`OnDelete` update strategy and two hashes.** A settings change must not restart a server in the middle of a game. With `OnDelete`, the new template applies on the next restart. A "restart hash" leaves out settings that are applied live over RCON, so only real restart-needing changes show the "restart required" banner.

**World settings applied over RCON.** On current Minecraft versions, difficulty, game mode and PvP are stored in the world, so `server.properties` alone does not change them. The manager sends `difficulty`, `defaultgamemode`, `whitelist on/off` and `gamerule pvp` after every start and on every change.

**Whitelist and ops synced from the database.** `EXISTING_WHITELIST_FILE` and `EXISTING_OPS_FILE` are `SYNCHRONIZE`, so the database list wins on each start. The Players tab also applies changes live over RCON.

**JVM heap at 75% of the memory limit.** The rest covers metaspace, threads and native memory. Without that headroom the pod gets OOMKilled. The ratio is configurable.

**Memory request equals limit.** The scheduler then reserves the full amount, and a game server is not evicted first under memory pressure.

**Namespaced Role only.** The manager can only touch its own namespace. It never gets a ClusterRole or admin kubeconfig.

**No `pods/exec`.** Backups and file editing would be easy with exec, but exec is broad and was outside the allowed resource list. A small sidecar sharing `/data` does the same with a narrow API and a per-server token.

**Helper pod for stopped servers.** A stopped server has no pod, so no sidecar. The manager starts a short-lived pod that mounts the volume, ends by itself after 30 minutes, and is removed as soon as the server starts. The only extra permission is `create` on pods.

## Networking and access

**mc-router instead of a port per server.** Every server shares port 25565; the hostname in the Minecraft handshake picks the server. No NodePort or LoadBalancer per server.

**Tailscale-only access in the homelab.** The only public path is a Cloudflare Tunnel, which carries HTTP, not raw Minecraft TCP. The UI and servers use `*.internal.fferrando.cc`, resolved by the local DNS for LAN and tailnet clients. Nothing forwards 25565 from the internet. For other setups, the chart just needs a DNS name pointing at the mc-router Service.

**Metrics on a separate port.** `/metrics` on `:9090` is never routed by the ingress, so it cannot leak through the UI host.

## Security choices

- argon2id with OWASP parameters for passwords; unknown users still run a hash so timing does not reveal usernames.
- Sessions stored as an HMAC of the cookie token, so a leaked sessions table cannot be replayed.
- CSRF: per-session token in a header plus an Origin check; WebSockets check the origin too.
- Other users' servers return 404, not 403, so their existence is not revealed.
- Admins cannot demote, disable or delete themselves, and the last active admin is protected.
- The file sidecar confines all paths with Go's `os.Root`, which also blocks symlink escapes.

## Features

**Auto sleep through mc-router.** mc-router already knows when a server is idle and when a player is trying to join, so it scales StatefulSets itself. Per-server opt-in uses Service annotations. Stopped servers get `autoScaleUp=false` so a connection cannot start a server its owner stopped. The reconciler stops enforcing replicas for sleeping servers.

**Modrinth for mods and plugins.** Its API is public and needs no key; CurseForge needs one. Only project slugs are stored. The itzg image downloads them (and required dependencies) on start, so kubecraft keeps no copies.

**Backups are downloads, not a schedule.** A tar.gz is built on demand by the sidecar. On a running server, autosave is paused and the world flushed first so the archive is consistent. Scheduled backups to external storage are left for later.

**playit.gg for public access, routed by port.** The homelab is behind CGNAT and Cloudflare Tunnels only carry HTTP, so a free TCP relay (playit.gg) publishes servers. The agent key cannot create tunnels (read only), so tunnels are made in the playit dashboard with the name `kubecraft-<slug>` and kubecraft discovers them. Each tunnel has its own local port on a small gateway next to the agent; the gateway sends a port only to its server through mc-router. Routing by port keeps forged hostnames from reaching other servers and keeps auto sleep working. Without playit, the single `serverBaseDomain` name is used for everything, so a future VPS only needs DNS and an ingress route.

**Server type fixed after creation.** Moving a world between loaders (for example Fabric to Paper) often breaks it, so the type is chosen once.
