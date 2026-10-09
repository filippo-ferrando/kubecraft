# ArgoCD and GitOps

The homelab repository uses the app-of-apps pattern: everything under `infrastructure/` and `apps/` is synced by two root Applications. kubecraft fits in like `lastgasp`: an ArgoCD Application that installs the OCI chart, with a values file kept in the homelab repo, plus the database objects and sealed secrets next to it.

## Files in the homelab repository

```
infrastructure/kubecraft/
├── repository.yaml         ArgoCD repository for the OCI registry (if not shared with lastgasp)
├── application.yaml        the chart, version pinned (examples/argocd-application.yaml)
├── values.yaml             examples/values-homelab.yaml
├── namespace.yaml          minecraft namespace
├── db.yaml                 CNPG DatabaseRole + Database (examples/cnpg-database.yaml)
├── db_secret_database.yaml sealed role password in the database namespace
├── db_secret_minecraft.yaml the same login in the minecraft namespace
└── secrets.yaml            sealed SESSION_SECRET and bootstrap admin
```

Plus one line in `apps/database/networkpolicy.yaml` to let the `minecraft` namespace reach Postgres.

The OCI repository `git.fferrando.cc/rdfilippo` is already registered for lastgasp (`infrastructure/lastgasp/repository.yaml`), so kubecraft can use the same one.

## Sync waves

| Wave | Objects |
|---|---|
| -3 | namespace |
| 0 | sealed secrets |
| 3 | DatabaseRole |
| 4 | Database |
| 5 | kubecraft Application |

The manager would cope with a missing database for about a minute anyway, but waves avoid noisy restarts on a fresh cluster.

## How changes flow

- **New kubecraft version**: tag in the kubecraft repo, CI publishes image and chart, then bump `targetRevision` in `application.yaml` (Renovate can do this, as for other charts).
- **Config change**: edit `values.yaml` in the homelab repo; ArgoCD syncs; the manager restarts with `Recreate`. Game servers keep running.
- **Servers**: created by users through the UI, never in git. ArgoCD does not see them, so `prune` never deletes worlds.

## Renovate

Renovate's ArgoCD manager reads `targetRevision` of Helm sources in `infrastructure/**`, so OCI chart bumps are picked up like the other charts. The default image follows the chart version, so no separate image rule is needed.
