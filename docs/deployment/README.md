# Deployment overview

kubecraft ships as one image and one Helm chart, both published by the release workflow:

- image `git.internal.fferrando.cc/rdfilippo/kubecraft:vX.Y.Z` (pulled as `git.fferrando.cc/rdfilippo/kubecraft`, same registry),
- chart `oci://git.internal.fferrando.cc/rdfilippo/kubecraft`, version `X.Y.Z`, whose default image is the matching tag.

## What must exist first

| Need | Why | Homelab |
|---|---|---|
| Kubernetes 1.28+ with a default or named StorageClass that can expand | World volumes | k3s + Longhorn (`longhorn`) |
| metrics-server | CPU/RAM in the UI (optional) | bundled with k3s |
| An ingress controller with TLS | The UI | Traefik + cert-manager (`letsencrypt-cloudflare`) |
| A LoadBalancer implementation, or NodePort | mc-router on 25565 | k3s ServiceLB |
| PostgreSQL 14+ and a login with an owned database | All metadata | CloudNativePG `homelab-db` |
| DNS for the UI host and `*.<serverBaseDomain>` | UI and players | AdGuard rewrite `*.internal.fferrando.cc` |
| Outbound HTTPS | Mojang, Modrinth, server jar downloads | yes |

kubecraft never deploys a database. Without `database.host` + `database.credentialsSecret`, or `database.urlSecret`, the chart refuses to render.

## Steps

1. **Database**: create a role and a database owned by it. With CloudNativePG see [examples/cnpg-database.yaml](examples/cnpg-database.yaml). If the database namespace has a NetworkPolicy, allow the kubecraft namespace.
2. **Secrets** in the kubecraft namespace:
   - the database credentials (`username`, `password`), the same values as the role,
   - `kubecraft-secrets` with `SESSION_SECRET`, `BOOTSTRAP_ADMIN_USER`, `BOOTSTRAP_ADMIN_PASSWORD`.
   Commands: [examples/secrets.sh](examples/secrets.sh).
3. **Values**: start from [examples/values-homelab.yaml](examples/values-homelab.yaml). All options: [chart reference](chart-reference.md).
4. **Install**: with ArgoCD ([examples/argocd-application.yaml](examples/argocd-application.yaml), see [ArgoCD and GitOps](argocd.md)) or by hand:
   ```bash
   helm install kubecraft oci://git.internal.fferrando.cc/rdfilippo/kubecraft \
     --version 0.1.0 -n minecraft --create-namespace -f values-homelab.yaml
   ```
5. **DNS**: point the UI host at the ingress and `*.<serverBaseDomain>` at the mc-router Service address (`kubectl -n minecraft get svc kubecraft-mc-router`).
6. **First login**: open the UI, log in as the bootstrap admin, set a new password, create users and quotas.
7. **Check**: create a small Paper server, wait for "Running", connect to `<slug>.<serverBaseDomain>`.

## What the install creates

```
namespace minecraft
├── Deployment kubecraft            manager (API, UI, reconciler, poller, metrics)
├── Service kubecraft               :80 -> 8080, for the ingress
├── Service kubecraft-metrics       :9090, for Prometheus
├── Service kubecraft-mc-router-metrics  :8080, only with mcRouter.metrics
├── IngressRoute / Ingress          UI host, TLS
├── Certificate                     optional, cert-manager
├── ServiceAccount + Role + RoleBinding (manager, namespaced)
├── Deployment kubecraft-mc-router
├── Service kubecraft-mc-router     LoadBalancer :25565
├── ServiceAccount + Role + RoleBinding (mc-router)
├── ResourceQuota + LimitRange      namespace backstop
├── NetworkPolicies                 manager, mc-router, game servers
├── ServiceMonitor                  optional
└── Secret kubecraft-secrets        only when secrets are given inline (testing)
```

Game servers are not part of the chart. The manager creates them at runtime (`mc-<slug>` StatefulSet, Service, PVC, Secret), so ArgoCD does not track or prune them. Uninstalling the chart leaves servers and worlds in place; delete them in the UI first, or remove them with `kubectl delete sts,svc,secret,pvc -l app.kubernetes.io/managed-by=mc-manager`.

## Startup order

The manager retries the database for about a minute and stays not-ready until both Postgres and the Kubernetes API answer, so it can start before the database is up. With ArgoCD sync waves, put the database role and database first (see the example Application).
