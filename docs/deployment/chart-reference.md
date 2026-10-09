# Chart reference

Chart: `charts/kubecraft`, published as `oci://git.internal.fferrando.cc/rdfilippo/kubecraft`.

## Values

### Image

| Value | Default | Notes |
|---|---|---|
| `image.repository` | `git.fferrando.cc/rdfilippo/kubecraft-manager` | Manager and file sidecar |
| `image.tag` | chart `appVersion` | Released charts pin the matching tag |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | Needed if the registry package is private |

### Manager

| Value | Default | Notes |
|---|---|---|
| `manager.uiHost` | `mine.example.com` | UI host; also the allowed origin for CSRF and WebSockets |
| `manager.serverBaseDomain` | `mine.example.com` | Servers get `<slug>.<this>` |
| `manager.cookieSecure` | `true` | `false` only for plain HTTP tests |
| `manager.sessionTTL` | `168h` | |
| `manager.allowedOrigins` | `""` | Extra origins, comma separated |
| `manager.jvmHeapRatio` | `"0.75"` | Heap share of the memory limit |
| `manager.defaultMaxServers` | `2` | Initial default quota; later changed in the UI |
| `manager.storageClass` | `""` | World volumes; empty uses the default class |
| `manager.minecraftImage` / `minecraftImageTag` | `itzg/minecraft-server` / `latest` | Pin a tag for reproducible servers |
| `manager.replicas` | `1` | Keep at 1 (see decisions) |
| `manager.logLevel` | `info` | |
| `manager.resources`, `extraEnv`, `nodeSelector`, `tolerations`, `affinity`, `podAnnotations` | | Usual pod settings |

### Secrets

| Value | Notes |
|---|---|
| `secrets.existingSecret` | Secret with `SESSION_SECRET` and optionally `BOOTSTRAP_ADMIN_USER`, `BOOTSTRAP_ADMIN_PASSWORD`. Use this in production |
| `secrets.sessionSecret`, `bootstrapAdminUser`, `bootstrapAdminPassword` | Only when `existingSecret` is empty; the chart then creates the Secret. For tests |

### Database (required)

| Value | Default | Notes |
|---|---|---|
| `database.host` | `""` | Option 1 |
| `database.port`, `name`, `sslmode` | `5432`, `kubecraft`, `prefer` | |
| `database.credentialsSecret.name` | `""` | Required with `host` |
| `database.credentialsSecret.usernameKey` / `passwordKey` | `username` / `password` | CloudNativePG layout |
| `database.urlSecret.name` / `key` | `""` / `DATABASE_URL` | Option 2: full URL |

### UI exposure

| Value | Default | Notes |
|---|---|---|
| `service.type` / `port` | `ClusterIP` / `80` | |
| `ingress.enabled`, `className`, `annotations`, `tls.enabled`, `tls.secretName` | off | Standard Ingress |
| `ingressRoute.enabled`, `entryPoints`, `middlewares`, `tls.secretName` | off, `[websecure]` | Traefik CRD |
| `certificate.enabled`, `secretName`, `issuerRef` | off | cert-manager Certificate for `uiHost` |

### mc-router

| Value | Default | Notes |
|---|---|---|
| `mcRouter.enabled` | `true` | |
| `mcRouter.image.tag` | `1.47.2` | |
| `mcRouter.service.type` | `LoadBalancer` | Or `NodePort` / `ClusterIP` |
| `mcRouter.service.port`, `nodePort`, `loadBalancerIP`, `externalTrafficPolicy`, `annotations` | `25565`, ... | |
| `mcRouter.autoScale.enabled` / `downAfter` | `true` / `15m` | Auto sleep; servers opt in |
| `mcRouter.metrics` | `false` | Prometheus metrics of mc-router |
| `mcRouter.extraArgs`, `resources`, `nodeSelector`, `tolerations`, `affinity` | | |

### Features and backstops

| Value | Default | Notes |
|---|---|---|
| `files.enabled` | `true` | File sidecar: backups and config editor |
| `metrics.enabled`, `port` | `true`, `9090` | Manager metrics |
| `metrics.serviceMonitor.enabled`, `interval`, `labels` | off, `30s`, `{}` | Set `labels.release` for kube-prometheus-stack |
| `resourceQuota.enabled`, `hard` | on | Namespace totals; size it for the plans offered |
| `limitRange.enabled`, `default`, `defaultRequest`, `max` | on | Defaults for pods without resources |
| `networkPolicy.enabled` | `true` | |
| `networkPolicy.managerFrom` | traefik namespace | Who may reach the UI |
| `networkPolicy.metricsFrom` | monitoring namespace | Who may scrape |

## Templates

| Template | Creates |
|---|---|
| `manager-deployment.yaml` | Manager Deployment; fails rendering without database settings |
| `manager-service.yaml` | UI Service |
| `manager-rbac.yaml` | ServiceAccount, namespaced Role (statefulsets, services, PVCs, secrets, configmaps, pods incl. create, pods/log, events read, pod metrics read), RoleBinding |
| `secret.yaml` | Only without `existingSecret` |
| `ingress.yaml` | Ingress, IngressRoute and Certificate, each optional |
| `mc-router.yaml` | mc-router ServiceAccount, Role (services; statefulsets patch with auto scale), Deployment, Service |
| `metrics.yaml` | Metrics Services and optional ServiceMonitor |
| `networkpolicy.yaml` | Manager: ingress namespace on 8080, scraper on 9090. mc-router: 25565 from anywhere. Game servers: 25565 from mc-router and manager, 25575 and 8081 from the manager |
| `quota.yaml` | ResourceQuota and LimitRange |

## Sizing the ResourceQuota

Each server pod reserves its plan's memory (request = limit) plus 32 MiB for the sidecar. With the defaults (`limits.memory: 24Gi`), about five Medium servers, or two Large ones, can run at the same time. Raise `resourceQuota.hard` to match the node capacity and the user quotas.
