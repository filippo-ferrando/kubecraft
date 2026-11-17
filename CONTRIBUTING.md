# Contributing to kubecraft

Bug reports, fixes, new features and documentation improvements are all welcome.

## Reporting bugs

Include:

- the kubecraft version (image tag and chart version),
- the Kubernetes distribution and version, and the ingress and storage in use,
- what you did, what you expected, and what happened,
- the manager logs (`kubectl -n <namespace> logs deploy/kubecraft`) and, for a game server
  problem, its state in the UI and `kubectl -n <namespace> describe pod mc-<slug>-0`.

Never paste secrets, session cookies or RCON passwords.

## Development setup

You need Go 1.27, Node 24+ and Docker. sqlc, golangci-lint, helm and Postgres run in Docker
through the `Makefile`. The full guide is in [docs/development.md](docs/development.md).

```sh
make run                 # manager on :8080, test Postgres in Docker, current kubeconfig
cd web && npm run dev    # UI on :5173 with hot reload
make test-integration    # Go tests with -race against a Postgres in Docker
make test-web            # svelte-check and vitest
make lint                # golangci-lint
make chart-lint          # helm lint
make sqlc                # regenerate queries after editing internal/db/queries
make e2e                 # k3s in Docker, chart install, a real Paper server
```

`make run` uses the current kube context, so point it at a test cluster.

## Pull requests

- Run `make lint test` before pushing. It must pass cleanly.
- Run `make e2e` for changes to the chart, the resource builders or the reconciler.
- Keep changes focused: one feature or fix per pull request.
- Every new feature needs a test, unit or integration. See the existing `*_test.go` files.
- Update the docs in `docs/` and the README when behavior, values or requirements change.

## Code and docs style

- Go code is formatted with `gofmt` and must pass `golangci-lint`.
- Every database query goes through sqlc. No SQL built from strings.
- Only the manager talks to the Kubernetes API, and its Role stays namespaced.
- Comments are at most 2 lines and explain why, not what the code does.
- Comments and docs use plain language, without em-dashes, jargon or marketing words.
