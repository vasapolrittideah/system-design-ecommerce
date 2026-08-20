# ==============================================================================
# system-design-ecommerce
#
# Run `make` or `make help` for the target list.
# ==============================================================================

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help
MAKEFLAGS += --no-print-directory

ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BIN_DIR  := $(ROOT_DIR)/bin

# Locally pinned tools win over whatever is installed on the machine.
export PATH  := $(BIN_DIR):$(PATH)
export GOBIN := $(BIN_DIR)

# ------------------------------------------------------------------------------
# Tool versions — bump here, then run `make tools`.
# ------------------------------------------------------------------------------
BUF_VERSION           ?= v1.72.0
SQLC_VERSION          ?= v1.31.1
MOCKERY_VERSION       ?= v3.7.3
GOOSE_VERSION         ?= v3.27.3
GOLANGCI_LINT_VERSION ?= v2.12.2

# ------------------------------------------------------------------------------
# Knobs
# ------------------------------------------------------------------------------
# Service selector for per-service targets: make migrate-up SVC=order
SVC ?=

# Local Kubernetes stack. The cluster is created once and outlives everything
# else; NAMESPACE is where both the infra and the services land.
CLUSTER   ?= ecommerce
NAMESPACE ?= ecommerce
K8S_DIR   := deploy/k8s
IMAGE_TAG ?= dev

# The k3d-managed registry Tilt pushes to. The name is also a hostname inside
# the cluster, so it may not contain characters a DNS label cannot.
REGISTRY  ?= ecommerce-registry:5001

# Local development database, reached through `make port-forward SVC=x`. Each
# service runs its own Postgres instance holding one database, so there is no
# other database on the far end of this connection to reach by accident.
#
# The password matches the secretGenerator in the service's local overlay. It
# is spelled out rather than read from the cluster so that migrating does not
# require kubectl, and overriding DSN replaces the whole string anyway.
DB_PASSWORD ?= insecure-local-only
DSN ?= postgres://$(SVC):$(DB_PASSWORD)@localhost:5432/$(SVC)?sslmode=disable

# Where `make smoke` points. The gateway on the k3d load balancer, never a
# port-forward: reaching bff-web directly would skip Kong's routes, its
# upstream, and the Service behind it, which is most of what a deploy breaks.
BASE_URL ?= http://localhost:8000

# `make deploy SVC=x SMOKE=0` skips the smoke test, for the one case where it is
# a false alarm: rolling out a service into a cluster that has no bff-web yet.
SMOKE ?= 1

# `make load` knobs. RATE is requests per second *offered* rather than achieved:
# the generator starts an iteration on a schedule instead of waiting for the last
# one, so a system that slows down builds a queue rather than receiving less
# traffic, which is the behaviour worth watching.
#
# LIMITER=keep leaves Kong's rate limits in place. The default raises them for
# the run and puts them back afterwards, because 300 requests a minute is a limit
# set for people and a run against it measures the limiter.
SCENARIO ?= me
RATE     ?= 50
DURATION ?= 1m
WARMUP   ?= 20s
USERS    ?= 10
LIMITER  ?= open

# `buf breaking` baseline. CI on a PR may want '.git\#branch=origin/trunk'.
# The backslash is required: an unescaped # starts a Make comment.
BREAKING_AGAINST ?= .git\#branch=trunk

# `make api-docs` reads this one. The REST contract is one file per BFF, so a
# second audience is `make api-docs SPEC=api/openapi/bff-admin.yaml`. Swagger UI
# arrives as an image rather than through `make tools`, and is pinned for the
# same reason everything else here is.
SPEC               ?= api/openapi/bff-web.yaml
SWAGGER_UI_VERSION ?= v5.32.14
SWAGGER_UI_PORT    ?= 8081

# Test knobs: make test PKG=./pkg/httpx/... GOTEST_FLAGS='-run TestBind -v'
PKG          ?= ./...
GOTEST_FLAGS ?=

COVERAGE_OUT  := coverage.out
COVERAGE_HTML := coverage.html

# Where each generator writes, as git pathspecs the *-check targets compare.
# Quoted at the point of use so git sees the pattern rather than the shell's
# expansion of it, and ending in /* because a pathspec containing a wildcard is
# matched against whole paths — without it the directory name matches nothing
# inside the directory, and the check passes on everything.
SQLC_OUT := services/*/internal/adapter/out/postgres/sqlc/*
MOCK_OUT := services/*/internal/port/*/mocks/*

# Fail early with an actionable message instead of "command not found".
define need
	@command -v $(1) >/dev/null 2>&1 || { echo "$(1) not found — run: make tools"; exit 1; }
endef

# Regenerate, then fail if anything moved: committed output that does not match
# its source is output nobody regenerated.
#
# --porcelain rather than `git diff`: a file that was never committed is
# untracked and would not show up in a diff at all. The message must contain no
# comma — $(call) splits its arguments on them.
define check_generated
	@if [ -n "$$(git status --porcelain -- $(1))" ]; then \
		git status --short -- $(1); \
		echo "$(2)"; \
		exit 1; \
	fi
endef

define need_svc
	@if [ -z "$(SVC)" ]; then echo "SVC is required, e.g. make $@ SVC=order"; exit 1; fi
endef

# k3d, kubectl, and docker are not Go programs, so `make tools` cannot install
# them. Each gets its own hint instead of a generic one.
define need_bin
	@command -v $(1) >/dev/null 2>&1 || { echo "$(1) not found — install with: $(2)"; exit 1; }
endef

# Generated ConfigMaps and Secrets carry fixed names, so kubectl apply does not
# roll a Deployment when only their content changed — Reloader does. Deploying
# without it means a rotated key or a changed setting reports success and takes
# effect on nothing.
define need_reloader
	@kubectl -n $(NAMESPACE) wait --for=condition=Available deployment/reloader-reloader --timeout=30s >/dev/null 2>&1 || { \
		echo "Reloader is not available in namespace '$(NAMESPACE)'."; \
		echo "Config and secret changes would silently fail to restart anything — run: make up"; \
		exit 1; \
	}
endef

define need_cluster
	$(call need_bin,k3d,brew install k3d)
	@k3d cluster list $(CLUSTER) >/dev/null 2>&1 || { echo "cluster '$(CLUSTER)' does not exist — run: make cluster-create"; exit 1; }
endef

##@ General

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m [VAR=value]\n"} \
		/^[a-zA-Z0-9_%-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)
	@echo

##@ Go

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: tidy-check
tidy-check: ## Fail if go.mod or go.sum is not tidy (CI)
	go mod tidy
	$(call check_generated,go.mod go.sum,go.mod or go.sum is not tidy — run: make tidy and commit the result)

.PHONY: deps
deps: ## Download and verify module dependencies
	go mod download
	go mod verify

.PHONY: fmt
fmt: ## Format all Go code
	$(call need,golangci-lint)
	golangci-lint fmt

.PHONY: fmt-check
fmt-check: ## Fail if any Go code is unformatted (CI)
	$(call need,golangci-lint)
	golangci-lint fmt --diff

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint
	$(call need,golangci-lint)
	golangci-lint run

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with autofixes applied
	$(call need,golangci-lint)
	golangci-lint run --fix

.PHONY: test
test: ## Run all tests with race detector and coverage (needs Docker for testcontainers)
	go test $(PKG) -race -cover $(GOTEST_FLAGS)

.PHONY: cover
cover: ## Run tests and open an HTML coverage report
	go test $(PKG) -race -covermode=atomic -coverprofile=$(COVERAGE_OUT) $(GOTEST_FLAGS)
	@grep -v '/gen/go/' $(COVERAGE_OUT) > $(COVERAGE_OUT).tmp && mv $(COVERAGE_OUT).tmp $(COVERAGE_OUT)
	go tool cover -func=$(COVERAGE_OUT) | tail -1
	go tool cover -html=$(COVERAGE_OUT) -o $(COVERAGE_HTML)
	@echo "wrote $(COVERAGE_HTML)"

.PHONY: bench
bench: ## Run benchmarks
	go test $(PKG) -run '^$$' -bench . -benchmem

.PHONY: generate
generate: ## Run go generate
	go generate ./...

.PHONY: mock
mock: ## Regenerate mocks (mockery v3, driven by .mockery.yml)
	$(call need,mockery)
	mockery

.PHONY: mock-check
mock-check: ## Fail if the committed mocks are stale relative to port/ (CI)
	$(call need,mockery)
	mockery
	$(call check_generated,"$(MOCK_OUT)",mocks are stale — run: make mock and commit the result)

.PHONY: build
build: ## Build every services/*/cmd/* binary into bin/
	@mkdir -p $(BIN_DIR)
	@mains=$$(find services -type f -path '*/cmd/*/main.go' 2>/dev/null | sort || true); \
	if [ -z "$$mains" ]; then echo "no services/*/cmd/*/main.go yet — nothing to build"; exit 0; fi; \
	for main in $$mains; do \
		svc=$$(echo $$main | cut -d/ -f2); \
		cmd=$$(basename $$(dirname $$main)); \
		echo "  build $$svc/$$cmd -> bin/$$svc-$$cmd"; \
		go build -o $(BIN_DIR)/$$svc-$$cmd ./services/$$svc/cmd/$$cmd; \
	done

##@ Proto (buf)

.PHONY: proto
proto: proto-lint proto-breaking proto-generate ## Lint, check breaking changes, then generate

.PHONY: proto-lint
proto-lint: ## buf lint
	$(call need,buf)
	buf lint

.PHONY: proto-format
proto-format: ## Format .proto files in place
	$(call need,buf)
	buf format -w

.PHONY: proto-breaking
proto-breaking: ## Check for breaking changes against trunk
	$(call need,buf)
	@# A baseline that contains no .proto files is the first proto commit, not
	@# a breaking change — but buf exits 1 on it either way, which would leave
	@# `make proto` failing for everyone until the first schema lands.
	@out=$$(buf breaking --against '$(BREAKING_AGAINST)' 2>&1) || { \
		case "$$out" in \
			*"had no .proto files"*) \
				echo "baseline has no .proto files yet — nothing to compare against" ;; \
			*) echo "$$out"; exit 1 ;; \
		esac; \
	}

.PHONY: proto-generate
proto-generate: ## buf generate into gen/go and docs/proto
	$(call need,buf)
	buf generate

.PHONY: proto-deps
proto-deps: ## Update buf.lock from buf.yaml dependencies
	$(call need,buf)
	buf dep update

.PHONY: proto-check
proto-check: ## Fail if gen/ or docs/proto is stale relative to proto/ (CI)
	$(call need,buf)
	buf generate
	$(call check_generated,gen/ docs/proto/,generated output is stale — run: make proto and commit the result)

##@ Database

.PHONY: sqlc
sqlc: ## Generate type-safe queries with sqlc
	$(call need,sqlc)
	sqlc generate

.PHONY: sqlc-check
sqlc-check: ## Fail if the sqlc output is stale relative to db/ (CI)
	$(call need,sqlc)
	sqlc generate
	$(call check_generated,"$(SQLC_OUT)",sqlc output is stale — run: make sqlc and commit the result)

.PHONY: sqlc-vet
sqlc-vet: ## Lint SQL queries with sqlc
	$(call need,sqlc)
	sqlc vet

.PHONY: migrate-up
migrate-up: ## Apply migrations (make migrate-up SVC=order [DSN=...])
	$(need_svc)
	$(call need,goose)
	goose -dir services/$(SVC)/db/migrations postgres "$(DSN)" up

.PHONY: migrate-down
migrate-down: ## Roll back the last migration (SVC=order)
	$(need_svc)
	$(call need,goose)
	goose -dir services/$(SVC)/db/migrations postgres "$(DSN)" down

.PHONY: migrate-status
migrate-status: ## Show migration status (SVC=order)
	$(need_svc)
	$(call need,goose)
	goose -dir services/$(SVC)/db/migrations postgres "$(DSN)" status

.PHONY: migrate-reset
migrate-reset: ## Roll back every migration (SVC=order) — destroys data
	$(need_svc)
	$(call need,goose)
	goose -dir services/$(SVC)/db/migrations postgres "$(DSN)" reset

.PHONY: migrate-create
migrate-create: ## Create a migration (make migrate-create SVC=order NAME=add_orders)
	$(need_svc)
	$(call need,goose)
	@if [ -z "$(NAME)" ]; then echo "NAME is required, e.g. make migrate-create SVC=order NAME=add_orders"; exit 1; fi
	@mkdir -p services/$(SVC)/db/migrations
	goose -dir services/$(SVC)/db/migrations create $(NAME) sql

##@ Local cluster

.PHONY: cluster-create
cluster-create: ## Create the k3d cluster (run once, survives reboots)
	$(call need_bin,k3d,brew install k3d)
	@# Traefik is disabled because Kong is this system's gateway, and two
	@# ingress controllers fighting over port 80 is a confusing first hour.
	@#
	@# The loadbalancer port mappings are declared now even though nothing
	@# listens on them yet: k3d cannot add a port mapping to an existing
	@# cluster, so leaving them out means recreating the cluster on the day
	@# Kong arrives.
	@#
	@# --registry-create publishes the local-registry-hosting ConfigMap that
	@# Tilt reads, so `tilt up` pushes there and the kubelet pulls from it.
	@# Without it every rebuild would go through `k3d image import`, which
	@# copies the whole image into each node on every change.
	k3d cluster create $(CLUSTER) \
		--agents 2 \
		--k3s-arg "--disable=traefik@server:*" \
		-p "8000:80@loadbalancer" \
		-p "8443:443@loadbalancer" \
		--registry-create $(REGISTRY) \
		--wait
	kubectl apply -f $(K8S_DIR)/infra/namespace.yaml
	kubectl config set-context --current --namespace=$(NAMESPACE)

.PHONY: cluster-delete
cluster-delete: ## Delete the k3d cluster and everything inside it
	$(call need_bin,k3d,brew install k3d)
	k3d cluster delete $(CLUSTER)

##@ Local stack

.PHONY: up
up: ## Start the shared local infra in the cluster (Jaeger, Loki, Alloy, Kong, Prometheus, Grafana, Reloader)
	$(need_cluster)
	@# Databases are not here: each service brings its own Postgres instance in
	@# its own overlay, so `make deploy SVC=x` is what starts x's database.
	kubectl apply -f $(K8S_DIR)/infra/namespace.yaml
	kubectl apply -k $(K8S_DIR)/infra
	kubectl -n $(NAMESPACE) rollout status deployment/jaeger --timeout=180s
	kubectl -n $(NAMESPACE) rollout status deployment/loki --timeout=180s
	@# A DaemonSet, so this waits for one Alloy per node. It is the only
	@# workload here whose replica count follows the cluster.
	kubectl -n $(NAMESPACE) rollout status daemonset/alloy --timeout=180s
	kubectl -n $(NAMESPACE) rollout status deployment/kong --timeout=180s
	kubectl -n $(NAMESPACE) rollout status deployment/prometheus --timeout=180s
	kubectl -n $(NAMESPACE) rollout status deployment/grafana --timeout=180s
	kubectl -n $(NAMESPACE) rollout status deployment/reloader-reloader --timeout=180s
	@echo "gateway: http://localhost:8000 (https://localhost:8443 — self-signed)"

.PHONY: down
down: ## Remove the shared local infra, keeping the namespace and every volume
	kubectl delete -k $(K8S_DIR)/infra --ignore-not-found

.PHONY: clean-volumes
clean-volumes: ## Delete the namespace and its volumes — destroys local data
	kubectl delete namespace $(NAMESPACE) --ignore-not-found

.PHONY: ps
ps: ## Show everything running in the namespace
	kubectl -n $(NAMESPACE) get pods,svc,job,hpa

.PHONY: logs
logs: ## Tail a service's logs across every replica (make logs SVC=identity)
	$(need_svc)
	kubectl -n $(NAMESPACE) logs -f --tail=100 --max-log-requests=10 \
		-l app.kubernetes.io/name=$(SVC)

.PHONY: port-forward
port-forward: ## Expose a service's database and the observability UIs on localhost (SVC=identity)
	$(need_svc)
	@if [ ! -d services/$(SVC)/db/migrations ]; then \
		echo "$(SVC) owns no database — nothing to forward. Use: make port-forward SVC=identity"; \
		exit 1; \
	fi
	@echo "$(SVC) postgres -> localhost:5432"
	@echo "jaeger UI       -> http://localhost:16686"
	@# 9092, because 9090 and 9091 are where the Tiltfile forwards the admin
	@# ports Prometheus is scraping.
	@echo "prometheus      -> http://localhost:9092 (alerts: /alerts)"
	@echo "grafana         -> http://localhost:3000"
	@# One database at a time on 5432, because that is what DSN and every
	@# psql invocation assume. Forwarding a second service means a second
	@# terminal with SVC set to it and a port of its own.
	@trap 'kill 0' EXIT; \
	kubectl -n $(NAMESPACE) port-forward svc/$(SVC)-postgres 5432:5432 >/dev/null & \
	kubectl -n $(NAMESPACE) port-forward svc/jaeger 16686:16686 >/dev/null & \
	kubectl -n $(NAMESPACE) port-forward svc/prometheus 9092:9090 >/dev/null & \
	kubectl -n $(NAMESPACE) port-forward svc/grafana 3000:3000 >/dev/null & \
	wait

##@ Develop

.PHONY: dev
dev: ## Run the Tilt development loop (ctrl-c to stop, leaves the cluster up)
	$(need_cluster)
	$(call need_bin,tilt,brew install tilt)
	tilt up

.PHONY: dev-down
dev-down: ## Remove everything Tilt deployed, keeping the cluster
	$(call need_bin,tilt,brew install tilt)
	tilt down

.PHONY: api-docs
api-docs: ## Browse a BFF's OpenAPI spec in Swagger UI (ctrl-c to stop, SPEC=...)
	$(call need_bin,docker,brew install --cask docker)
	@[ -f $(SPEC) ] || { echo "$(SPEC) does not exist"; exit 1; }
	@echo "$(notdir $(SPEC)) -> http://localhost:$(SWAGGER_UI_PORT)"
	@# Reading only. "Try it out" posts from this origin to Kong on :8000, and
	@# the CORS plugin in kong.yml allows the storefront's origin alone — so the
	@# browser blocks it, and widening that list is a gateway change to make
	@# deliberately rather than a side effect of opening the docs.
	@docker run --rm --name swagger-ui \
		-p $(SWAGGER_UI_PORT):8080 \
		-e SWAGGER_JSON=/spec/$(notdir $(SPEC)) \
		-v $(ROOT_DIR)/$(dir $(SPEC)):/spec:ro \
		swaggerapi/swagger-ui:$(SWAGGER_UI_VERSION)

##@ Deploy

.PHONY: keys
keys: ## Generate the local ES256 keypair and hand the public half to every verifier (gitignored)
	@# Every process that verifies a token needs the public half in its own
	@# overlay directory. kustomize refuses to read a file outside its root, so
	@# it is copied rather than referenced — add a directory here when a second
	@# BFF or anything else starts verifying.
	@signer=$(K8S_DIR)/overlays/local/identity; \
	verifiers="$(K8S_DIR)/overlays/local/bff-web"; \
	if [ -f $$signer/jwt-private.pem ]; then \
		echo "$$signer/jwt-private.pem already exists — delete it to rotate"; \
	else \
		openssl ecparam -name prime256v1 -genkey -noout -out $$signer/jwt-private.pem; \
		echo "wrote $$signer/jwt-private.pem"; \
	fi; \
	openssl ec -in $$signer/jwt-private.pem -pubout -out $$signer/jwt-public.pem 2>/dev/null; \
	for dir in $$verifiers; do \
		cp $$signer/jwt-public.pem $$dir/jwt-public.pem; \
		echo "wrote $$dir/jwt-public.pem"; \
	done

.PHONY: image
image: ## Build a service's images and import them into the cluster (SVC=identity)
	$(need_svc)
	$(need_cluster)
	$(call need_bin,docker,brew install --cask docker)
	@# Build context is the repo root: one go.mod covers every service, so a
	@# context scoped to services/$(SVC) cannot see the module it belongs to.
	docker build -f services/$(SVC)/Dockerfile --target server \
		-t ecommerce/$(SVC):$(IMAGE_TAG) .
	@# The migrate image exists only for a service that owns a database. The
	@# presence of a migrations directory is what says so — the Composition
	@# API has neither, and its Dockerfile has no such stage to build.
	@images="ecommerce/$(SVC):$(IMAGE_TAG)"; \
	if [ -d services/$(SVC)/db/migrations ]; then \
		docker build -f services/$(SVC)/Dockerfile --target migrate \
			-t ecommerce/$(SVC)-migrate:$(IMAGE_TAG) . || exit 1; \
		images="$$images ecommerce/$(SVC)-migrate:$(IMAGE_TAG)"; \
	fi; \
	k3d image import -c $(CLUSTER) $$images

.PHONY: deploy
deploy: ## Build, migrate, and roll out a service (make deploy SVC=identity)
	$(need_svc)
	$(need_reloader)
	$(MAKE) image SVC=$(SVC)
	@# Kustomize has no hooks, so the migration is ordered here instead. The
	@# Job is immutable once created, which is why it is deleted rather than
	@# re-applied — a changed image on an existing Job is rejected outright.
	@# A service with no migrations directory owns no database and has no Job.
	@if [ -d services/$(SVC)/db/migrations ]; then \
		kubectl -n $(NAMESPACE) delete job $(SVC)-migrate --ignore-not-found; \
	fi
	kubectl apply -k $(K8S_DIR)/overlays/local/$(SVC)
	@if [ -d services/$(SVC)/db/migrations ]; then \
		kubectl -n $(NAMESPACE) wait --for=condition=complete job/$(SVC)-migrate --timeout=180s || { \
			echo "--- migration failed ---"; \
			kubectl -n $(NAMESPACE) logs job/$(SVC)-migrate --tail=50; \
			exit 1; \
		}; \
	fi
	@# Every Deployment carrying the service's label, not just the one named
	@# after it. inventory ships a second one — its reservation reaper — and a
	@# deploy that waited only on `deployment/$(SVC)` would report success while
	@# that workload was still crash-looping on a bad config.
	@for deploy in $$(kubectl -n $(NAMESPACE) get deployments \
			-l app.kubernetes.io/name=$(SVC) -o name); do \
		kubectl -n $(NAMESPACE) rollout status $$deploy --timeout=180s || exit 1; \
	done
	@# rollout status means the new pods are Ready, which is a claim each pod
	@# makes about itself. Whether the system still serves is a different
	@# question, and this is where it gets asked.
	@if [ "$(SMOKE)" = "1" ]; then $(MAKE) smoke; else echo "smoke skipped (SMOKE=0)"; fi

.PHONY: smoke
smoke: ## Smoke-test the deployed stack through the gateway (BASE_URL=...)
	$(call need_bin,jq,brew install jq)
	@BASE_URL="$(BASE_URL)" scripts/smoke.sh

.PHONY: undeploy
undeploy: ## Remove a service from the cluster (SVC=identity)
	$(need_svc)
	kubectl delete -k $(K8S_DIR)/overlays/local/$(SVC) --ignore-not-found

.PHONY: restart
restart: ## Roll a service's pods without rebuilding (SVC=identity)
	$(need_svc)
	@for deploy in $$(kubectl -n $(NAMESPACE) get deployments \
			-l app.kubernetes.io/name=$(SVC) -o name); do \
		kubectl -n $(NAMESPACE) rollout restart $$deploy; \
		kubectl -n $(NAMESPACE) rollout status $$deploy --timeout=180s || exit 1; \
	done

.PHONY: render
render: ## Print the manifests an overlay would apply (SVC=identity)
	$(need_svc)
	kubectl kustomize $(K8S_DIR)/overlays/local/$(SVC)

##@ Load

.PHONY: load
load: ## Load-test the deployed stack through the gateway (SCENARIO=me|login RATE=50 DURATION=1m)
	$(call need_bin,k6,brew install k6)
	@# Deliberately not part of `make deploy` and not in CI. It takes minutes,
	@# it restarts the gateway twice to move the rate limits and back, and its
	@# numbers are about a laptop — a gate built on that is a flaky test.
	@SCENARIO="$(SCENARIO)" BASE_URL="$(BASE_URL)" NAMESPACE="$(NAMESPACE)" \
		RATE="$(RATE)" DURATION="$(DURATION)" WARMUP="$(WARMUP)" USERS="$(USERS)" \
		LIMITER="$(LIMITER)" scripts/load/run.sh

##@ Tools

.PHONY: tools
tools: ## Install pinned dev tools into bin/
	@mkdir -p $(BIN_DIR)
	go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	go install github.com/vektra/mockery/v3@$(MOCKERY_VERSION)
	go install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@echo "installed into $(BIN_DIR)"

.PHONY: tools-clean
tools-clean: ## Remove everything installed in bin/
	rm -rf $(BIN_DIR)/*

##@ Meta

.PHONY: ci
ci: tidy-check fmt-check lint proto-lint proto-check sqlc-vet sqlc-check mock-check test ## What CI runs
	@# proto-breaking is missing on purpose: the baseline here is trunk, which
	@# on trunk is the commit being checked. The workflow passes its own.

.PHONY: print-%
print-%: ## Print a variable's value (make print-BUF_VERSION) — lets CI read the pinned versions
	@echo '$($*)'

.PHONY: clean
clean: ## Remove build and coverage output
	rm -f $(COVERAGE_OUT) $(COVERAGE_HTML)
	@mains=$$(find services -type f -path '*/cmd/*/main.go' 2>/dev/null | sort || true); \
	for main in $$mains; do \
		svc=$$(echo $$main | cut -d/ -f2); \
		cmd=$$(basename $$(dirname $$main)); \
		rm -f $(BIN_DIR)/$$svc-$$cmd; \
	done
	go clean -testcache
