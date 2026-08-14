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
CMD ?= server

# Local Kubernetes stack. The cluster is created once and outlives everything
# else; NAMESPACE is where both the infra and the services land.
CLUSTER   ?= ecommerce
NAMESPACE ?= ecommerce
K8S_DIR   := deploy/k8s
IMAGE_TAG ?= dev

# Local development database, reached through `make port-forward`. Each service
# connects as its own role — that is what makes database-per-service a
# permission error rather than a code review comment.
DSN ?= postgres://$(SVC):$(SVC)@localhost:5432/$(SVC)?sslmode=disable

# `buf breaking` baseline. CI on a PR may want '.git\#branch=origin/trunk'.
# The backslash is required: an unescaped # starts a Make comment.
BREAKING_AGAINST ?= .git\#branch=trunk

# Test knobs: make test PKG=./pkg/httpx/... GOTEST_FLAGS='-run TestBind -v'
PKG          ?= ./...
GOTEST_FLAGS ?=

COVERAGE_OUT  := coverage.out
COVERAGE_HTML := coverage.html

# Fail early with an actionable message instead of "command not found".
define need
	@command -v $(1) >/dev/null 2>&1 || { echo "$(1) not found — run: make tools"; exit 1; }
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

.PHONY: run
run: ## Run one service command (make run SVC=order CMD=server)
	$(need_svc)
	go run ./services/$(SVC)/cmd/$(CMD)

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
	buf breaking --against '$(BREAKING_AGAINST)'

.PHONY: proto-generate
proto-generate: ## buf generate into gen/go
	$(call need,buf)
	buf generate

.PHONY: proto-deps
proto-deps: ## Update buf.lock from buf.yaml dependencies
	$(call need,buf)
	buf dep update

.PHONY: proto-check
proto-check: ## Fail if gen/go is stale relative to proto/ (CI)
	$(call need,buf)
	buf generate
	@# --porcelain, not `git diff`: a never-committed .pb.go is untracked and
	@# would not show up in a diff at all.
	@if [ -n "$$(git status --porcelain -- gen/)" ]; then \
		git status --short -- gen/; \
		echo "gen/ is stale — run: make proto and commit the result"; \
		exit 1; \
	fi

##@ Database

.PHONY: sqlc
sqlc: ## Generate type-safe queries with sqlc
	$(call need,sqlc)
	sqlc generate

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
	k3d cluster create $(CLUSTER) \
		--agents 2 \
		--k3s-arg "--disable=traefik@server:*" \
		-p "8000:80@loadbalancer" \
		-p "8443:443@loadbalancer" \
		--wait
	kubectl apply -f $(K8S_DIR)/infra/namespace.yaml
	kubectl config set-context --current --namespace=$(NAMESPACE)

.PHONY: cluster-delete
cluster-delete: ## Delete the k3d cluster and everything inside it
	$(call need_bin,k3d,brew install k3d)
	k3d cluster delete $(CLUSTER)

##@ Local stack

.PHONY: up
up: ## Start local infra in the cluster (Postgres, Jaeger)
	$(need_cluster)
	kubectl apply -f $(K8S_DIR)/infra/namespace.yaml
	kubectl apply -k $(K8S_DIR)/infra
	kubectl -n $(NAMESPACE) rollout status statefulset/postgres --timeout=180s
	kubectl -n $(NAMESPACE) rollout status deployment/jaeger --timeout=180s
	kubectl -n $(NAMESPACE) rollout status deployment/reloader-reloader --timeout=180s

.PHONY: down
down: ## Remove local infra, keeping the namespace and the database volume
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
port-forward: ## Expose infra on localhost for host-run services (ctrl-c to stop)
	@echo "postgres  -> localhost:5432"
	@echo "jaeger UI -> http://localhost:16686"
	@trap 'kill 0' EXIT; \
	kubectl -n $(NAMESPACE) port-forward svc/postgres 5432:5432 >/dev/null & \
	kubectl -n $(NAMESPACE) port-forward svc/jaeger 16686:16686 >/dev/null & \
	wait

##@ Deploy

.PHONY: keys
keys: ## Generate the local ES256 signing keypair for identity (gitignored)
	@dir=$(K8S_DIR)/overlays/local/identity; \
	if [ -f $$dir/jwt-private.pem ]; then \
		echo "$$dir/jwt-private.pem already exists — delete it to rotate"; exit 0; \
	fi; \
	openssl ecparam -name prime256v1 -genkey -noout -out $$dir/jwt-private.pem; \
	openssl ec -in $$dir/jwt-private.pem -pubout -out $$dir/jwt-public.pem 2>/dev/null; \
	echo "wrote $$dir/jwt-{private,public}.pem"

.PHONY: image
image: ## Build a service's images and import them into the cluster (SVC=identity)
	$(need_svc)
	$(need_cluster)
	$(call need_bin,docker,brew install --cask docker)
	@# Build context is the repo root: one go.mod covers every service, so a
	@# context scoped to services/$(SVC) cannot see the module it belongs to.
	docker build -f services/$(SVC)/Dockerfile --target server \
		-t ecommerce/$(SVC):$(IMAGE_TAG) .
	docker build -f services/$(SVC)/Dockerfile --target migrate \
		-t ecommerce/$(SVC)-migrate:$(IMAGE_TAG) .
	k3d image import -c $(CLUSTER) \
		ecommerce/$(SVC):$(IMAGE_TAG) ecommerce/$(SVC)-migrate:$(IMAGE_TAG)

.PHONY: deploy
deploy: ## Build, migrate, and roll out a service (make deploy SVC=identity)
	$(need_svc)
	$(need_reloader)
	$(MAKE) image SVC=$(SVC)
	@# Kustomize has no hooks, so the migration is ordered here instead. The
	@# Job is immutable once created, which is why it is deleted rather than
	@# re-applied — a changed image on an existing Job is rejected outright.
	kubectl -n $(NAMESPACE) delete job $(SVC)-migrate --ignore-not-found
	kubectl apply -k $(K8S_DIR)/overlays/local/$(SVC)
	@kubectl -n $(NAMESPACE) wait --for=condition=complete job/$(SVC)-migrate --timeout=180s || { \
		echo "--- migration failed ---"; \
		kubectl -n $(NAMESPACE) logs job/$(SVC)-migrate --tail=50; \
		exit 1; \
	}
	kubectl -n $(NAMESPACE) rollout status deployment/$(SVC) --timeout=180s

.PHONY: undeploy
undeploy: ## Remove a service from the cluster (SVC=identity)
	$(need_svc)
	kubectl delete -k $(K8S_DIR)/overlays/local/$(SVC) --ignore-not-found

.PHONY: restart
restart: ## Roll a service's pods without rebuilding (SVC=identity)
	$(need_svc)
	kubectl -n $(NAMESPACE) rollout restart deployment/$(SVC)
	kubectl -n $(NAMESPACE) rollout status deployment/$(SVC) --timeout=180s

.PHONY: render
render: ## Print the manifests an overlay would apply (SVC=identity)
	$(need_svc)
	kubectl kustomize $(K8S_DIR)/overlays/local/$(SVC)

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
ci: fmt-check lint proto-lint test ## What CI runs

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
