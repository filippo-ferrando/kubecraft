# Development

Requirements: Go 1.27, Node 24+, Docker. sqlc, golangci-lint, helm and Postgres run in Docker through the Makefile.

## Run locally

```bash
make run                 # manager on :8080, test Postgres in Docker, current kubeconfig
cd web && npm run dev    # UI on :5173 with hot reload, proxies /api to :8080
```

`make run` creates the bootstrap admin `admin` / `admin-password` (a password change is forced at first login). It uses the current kube context, so point it at a test cluster.

## Tests

| Command | Runs |
|---|---|
| `make test-unit` | Go tests; database tests skip without `TEST_DATABASE_URL` |
| `make test-integration` | Starts Postgres in Docker, all Go tests with `-race` |
| `make test-web` | svelte-check and vitest |
| `make lint` | golangci-lint |
| `make chart-lint` / `chart-lint-local` | helm lint; the local one also checks that the chart refuses to render without database settings |
| `make e2e` | Full system test in a throwaway k3s container |

What is covered:

- **Unit**: config, passwords, rate limit, slug/version/settings validation, quota math, resource builders, state mapping, auto sleep, file sidecar (including path escapes), RCON pool, player list parsing, Modrinth facets and validation, metrics collector.
- **API integration** (real Postgres, fake Kubernetes, fake RCON, fake Modrinth): login, CSRF, forced password change, quotas (including a parallel create race), cross-user isolation, lifecycle, reconciler repair and orphans, WebSockets, players, admin guards, catalog, files and backups.
- **e2e** (`hack/e2e.sh`): k3s in Docker, chart install, Paper server with a Modrinth plugin, status ping through mc-router by hostname, limits and heap, metrics scrape, config edit, world backups (running and stopped), stop/start, auto sleep and wake on a real login handshake, delete. Uses its own kubeconfig, never the current context. `KEEP=1` keeps the cluster.

Every new feature needs a test (see `CLAUDE.md`).

## Generated code

After editing `internal/db/queries/*.sql` or migrations, run `make sqlc` and commit the result.

## CI and releases

Forgejo Actions in `.forgejo/workflows`:

- `ci.yaml` (pushes to main, pull requests): gofmt, vet, golangci-lint, Go tests with a Postgres service, web checks and build, chart lint, image build without push.
- `release.yaml` (tags `v*.*.*`): tests, then pushes the image to `git.internal.fferrando.cc/rdfilippo/kubecraft` and the chart to `oci://git.internal.fferrando.cc/rdfilippo/kubecraft`, then publishes a release with the commits since the previous tag.

The repository secret `LOCAL_TOKEN` (access token with `write:package`) is required. `make e2e` needs a privileged Docker daemon, so it is a manual check before tagging.

```bash
make e2e
git tag v0.2.0 && git push origin v0.2.0
```
