# Mods and plugins

Code: `internal/modrinth`, `internal/api/handlers_catalog.go`, `web/src/components/ContentPicker.svelte`.

## Server types

| Group | Types | Content |
|---|---|---|
| Vanilla | VANILLA | none |
| Plugins | PAPER, PURPUR | Bukkit/Spigot/Paper plugins |
| Mods | FABRIC, FORGE, NEOFORGE | mods for that loader |

The type is fixed after creation.

## Catalog

The browser calls `/api/catalog/search`; the manager queries the Modrinth API with:

- `project_type:plugin` or `project_type:mod`,
- the loader categories of the type (Paper: paper, spigot, bukkit; Purpur adds purpur; Fabric: fabric; and so on),
- `server_side:required` or `optional` (client-only mods are hidden),
- the game version, when a specific one is chosen. For LATEST, the latest Mojang release is used; Paper can lag a little behind it, so a few plugins may be missing from results.

Responses are cached for 10 minutes. The manager sends a `User-Agent` as Modrinth asks. Icons load in the browser from `cdn.modrinth.com` (allowed by the Content Security Policy).

## Storage and install

Only Modrinth slugs are stored, in the server settings (`modrinth_projects`). They become `MODRINTH_PROJECTS` with `MODRINTH_DOWNLOAD_DEPENDENCIES=required`. The itzg image downloads the latest compatible version on each start and removes projects no longer listed. kubecraft keeps no copies of any file.

## Validation

On save (create, or when the list changes), the manager asks Modrinth for each project and refuses it when it does not exist, does not run on servers, or does not support the loader. Slugs and ids are normalized to slugs. Vanilla servers cannot have any. If Modrinth is down, saving a changed list fails with a clear message; other settings still save.

## Network needs

Manager: HTTPS to `api.modrinth.com`. Server pods: HTTPS to Modrinth's CDN on start. Browsers: `cdn.modrinth.com` for icons.
