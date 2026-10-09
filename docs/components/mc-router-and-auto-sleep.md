# mc-router and auto sleep

Chart template: `templates/mc-router.yaml`. Server side: Service annotations in `internal/k8s/builders.go`.

## Routing

A Minecraft client sends the hostname it connects to in its first packet. mc-router reads it and forwards the connection to the Service with a matching `mc-router.itzg.me/externalServerName` annotation. All servers share one port, 25565.

mc-router runs with `--in-kube-cluster` and `KUBE_NAMESPACE` set to its own namespace, so it only watches Services there. Its Role can read Services, and with auto scale also patch StatefulSets.

## Exposure

The chart exposes mc-router with a Service of type `LoadBalancer` (k3s ServiceLB listens on every node), configurable as `NodePort` or `ClusterIP`. A wildcard DNS name `*.<serverBaseDomain>` must point at that address. Unknown hostnames are refused.

Homelab setup: `*.internal.fferrando.cc` resolves to a node IP for LAN and tailnet clients; no port is forwarded from the internet, so servers are reachable only from there. With public DNS on Cloudflare, the record must be DNS only, since Minecraft traffic is raw TCP.

## Auto sleep

Enabled cluster-wide with `mcRouter.autoScale.enabled` (default on), which adds `--auto-scale-up --auto-scale-down --auto-scale-down-after=<downAfter>`. Each server opts in with its "Auto sleep" setting.

| Server | autoScaleUp | autoScaleDown |
|---|---|---|
| Running, auto sleep on | true | true |
| Running, auto sleep off | false | false |
| Stopped | false | false |

- After `downAfter` without connections, mc-router sets replicas to 0. The reconciler sees replicas 0 with auto sleep and reports `sleeping`; it does not scale it back.
- A status ping shows "<name> is sleeping, join to wake it up".
- A join attempt sets replicas to 1. The client is disconnected while the server boots; the player joins again after about 30 seconds.
- "Wake up" in the UI (the start action) scales to 1 directly.
- Stop sets both annotations to `false` and replicas to 0, so a stopped server never wakes from a connection.
- Quota counts a sleeping server as running, since it can wake at any time.
- After waking, the live poller applies world settings to the new pod.
