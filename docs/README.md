# kubecraft documentation

kubecraft is a web app that lets users create and run their own Minecraft servers on a k3s cluster. This folder explains every moving part, why it is built the way it is, and how to deploy it with the Helm chart.

## Start here

- [Architecture](architecture.md): the parts, how a request flows, and where state lives.
- [Design decisions](decisions.md): every main choice, the alternatives, and the reason.

## Components

| Page | Covers |
|---|---|
| [Manager and API](components/manager-api.md) | HTTP API, routes, auth, sessions, CSRF, roles, audit log |
| [Database](components/database.md) | Postgres schema, migrations, sqlc, connection options |
| [Server resources](components/server-resources.md) | What is created in Kubernetes for each server and why |
| [Reconciler](components/reconciler.md) | How database and cluster are kept in sync, states, deletes, orphans |
| [mc-router and auto sleep](components/mc-router-and-auto-sleep.md) | Hostname routing, exposure, sleeping servers |
| [Console, logs and live data](components/console-logs-live.md) | WebSockets, RCON, player list, CPU/RAM, settings applied live |
| [Files and backups](components/files-and-backups.md) | File sidecar, helper pod, config editor, backup downloads |
| [Quotas and plans](components/quotas-and-plans.md) | Plans, per-user limits, how checks avoid races |
| [Mods and plugins](components/mods-and-plugins.md) | Modrinth catalog, validation, how files reach the server |
| [playit.gg](components/playit.md) | Optional public access behind CGNAT: managed tunnels, gateway, routing by port |
| [Metrics](components/metrics.md) | Prometheus endpoint, series, alerts |
| [Frontend](components/frontend.md) | Svelte app, pages, how it talks to the API |

## Deployment

- [Deployment overview](deployment/README.md): how a chart install turns into a running system, step by step.
- [Chart reference](deployment/chart-reference.md): every value, and what each template creates.
- [ArgoCD and GitOps](deployment/argocd.md): deploying from the OCI chart with ArgoCD.
- [Upgrades and releases](deployment/upgrades.md): versions, migrations, rollbacks.
- [Examples](deployment/examples/): values file, ArgoCD Application, CloudNativePG database, secret commands.

## Working on the code

- [Development](development.md): local run, tests, end-to-end test, CI and releases.
