# Upgrades and releases

## Versions

A git tag `vX.Y.Z` produces image tags `vX.Y.Z`, `X.Y.Z`, `X.Y`, `latest` and chart version `X.Y.Z` with `appVersion: vX.Y.Z`. Pre-releases (`vX.Y.Z-rc1`) only get their own tag. Details in [development](../development.md#ci-and-releases).

## Upgrading

1. Read the release notes (commits since the previous tag).
2. Bump the chart version (ArgoCD `targetRevision` or `helm upgrade --version`).
3. The manager is replaced (`Recreate`), runs new migrations on start, and the reconciler re-applies server specs.

Game servers keep running during a manager upgrade. If the new version changes the server pod template (for example a new sidecar setting), each running server shows "restart required"; restart them when it suits the players.

## Database migrations

Migrations only move forward and run on start. Before a version with schema changes, a CloudNativePG backup is the rollback path. Rolling the chart back to an older version with a newer schema is not supported.

## Rolling back

- Without schema changes: set the previous chart version.
- With schema changes: restore the database backup taken before the upgrade, then set the previous version.

Worlds are never touched by manager upgrades or rollbacks; they live on the server volumes.

## Minecraft and mc-router versions

- `manager.minecraftImageTag` (default `latest`) decides the itzg image for all servers. Pin it to avoid surprise changes; servers pick up a new tag on their next restart.
- The Minecraft version itself is a per-server setting (`LATEST` or a fixed version).
- `mcRouter.image.tag` is pinned in the chart and bumped with chart releases.
