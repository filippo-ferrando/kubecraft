# Frontend

Code: `web/` (Svelte 5, Vite, Tailwind 4, xterm.js). Built to `web/dist` and embedded in the binary.

## Pages

| Path | Page |
|---|---|
| `/login` | Login |
| `/` | Dashboard: quota bars, server cards with state, players, CPU/RAM, address with copy, start/stop |
| `/servers/new` | Wizard: name and address (live check), type, version, mods or plugins, plan (shows what the quota allows), settings, EULA |
| `/servers/:id/:tab` | Server: Overview, Console, Players, Mods or Plugins, Settings, Files, Backups, Danger zone |
| `/account` | Own password (forced after bootstrap or reset) |
| `/admin/:tab` | Users and quotas, Plans, All servers, Audit log |

## How it works

- A tiny router on the History API; links use `use:link`.
- `lib/api.ts` wraps fetch: JSON in and out, CSRF header on writes, 401 drops back to login.
- Server pages poll every 3 seconds, the dashboard every 4, and pause while the tab is hidden.
- The console loads xterm.js on demand, so the main bundle stays around 40 KB gzipped. The socket reconnects with backoff.
- Dark theme by default, light toggle stored in `localStorage` and applied before the first paint.
- Works on mobile (collapsible nav, single-column cards).

## Development

`npm run dev` serves on :5173 and proxies `/api` (including WebSockets) to a manager on :8080. `make run` starts that manager against the test database with `ALLOWED_ORIGINS=http://localhost:5173`.

Checks: `npm run check` (svelte-check with TypeScript), `npm test` (vitest).
