IMAGE ?= git.fferrando.cc/rdfilippo/kubecraft-manager
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
TEST_DB_PORT ?= 55432
TEST_DATABASE_URL ?= postgres://test:test@localhost:$(TEST_DB_PORT)/test?sslmode=disable
UID := $(shell id -u)
GID := $(shell id -g)

.PHONY: all build web test test-unit test-integration test-web e2e lint sqlc run docker docker-push chart-lint chart-lint-local testdb testdb-stop clean

all: lint test build

web:
	cd web && npm ci --no-audit --no-fund && npm run build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/manager ./cmd/manager

# Unit tests only; integration tests skip without TEST_DATABASE_URL.
test-unit:
	go test ./...

# Starts a throwaway Postgres in Docker and runs every Go test against it.
test-integration: testdb
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race -count=1 ./...

test-web:
	cd web && npm run check && npm test

test: test-integration test-web

# Full cluster test in a throwaway k3s container (takes a few minutes).
e2e:
	./hack/e2e.sh

testdb:
	@docker inspect kubecraft-testpg >/dev/null 2>&1 || \
		docker run -d --name kubecraft-testpg -e POSTGRES_USER=test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=test \
		-p $(TEST_DB_PORT):5432 postgres:17-alpine >/dev/null
	@until docker exec kubecraft-testpg pg_isready -U test >/dev/null 2>&1; do sleep 1; done

testdb-stop:
	-docker rm -f kubecraft-testpg

lint:
	docker run --rm -v "$(CURDIR):/src" -w /src -u $(UID):$(GID) -e GOCACHE=/tmp/gocache -e GOLANGCI_LINT_CACHE=/tmp/lintcache \
		-e GOMODCACHE=/tmp/gomod golangci/golangci-lint:latest golangci-lint run ./...

sqlc:
	docker run --rm -v "$(CURDIR):/src" -w /src -u $(UID):$(GID) sqlc/sqlc:latest generate

chart-lint:
	docker run --rm -v "$(CURDIR)/charts:/apps" -w /apps alpine/helm:latest lint kubecraft \
		--set secrets.sessionSecret=x --set database.host=db --set database.credentialsSecret.name=db

# Same checks with a local helm binary (used by CI).
CHART_SETS = --set secrets.sessionSecret=x --set database.host=db --set database.credentialsSecret.name=db
chart-lint-local:
	helm lint charts/kubecraft $(CHART_SETS)
	helm lint charts/kubecraft $(CHART_SETS) --set ingressRoute.enabled=true --set certificate.enabled=true \
		--set metrics.serviceMonitor.enabled=true --set mcRouter.metrics=true
	@! helm template charts/kubecraft >/dev/null 2>&1 || (echo "chart must fail without database settings" >&2; exit 1)

# Runs the manager locally against the test database and the current kubeconfig.
run: build testdb
	DATABASE_URL="$(TEST_DATABASE_URL)" SESSION_SECRET=dev-session-secret-dev-session-secret \
	COOKIE_SECURE=false IN_CLUSTER=false UI_HOST=localhost:8080 ALLOWED_ORIGINS=http://localhost:5173 \
	BOOTSTRAP_ADMIN_USER=admin BOOTSTRAP_ADMIN_PASSWORD=admin-password LOG_LEVEL=debug ./bin/manager

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

docker-push: docker
	docker push $(IMAGE):$(VERSION)

clean:
	rm -rf bin web/dist/assets web/dist/index.html
