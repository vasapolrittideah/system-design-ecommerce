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

# Local development database. Override for anything else.
DSN ?= postgres://postgres:postgres@localhost:5432/$(SVC)?sslmode=disable

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

##@ Local stack

.PHONY: up
up: ## Start the local stack (Postgres, Kafka, Kong, otel…)
	docker compose up -d --build

.PHONY: down
down: ## Stop the local stack
	docker compose down

.PHONY: clean-volumes
clean-volumes: ## Stop the local stack and delete its volumes — destroys local data
	docker compose down -v

.PHONY: ps
ps: ## Show local stack containers
	docker compose ps

.PHONY: logs
logs: ## Tail local stack logs (make logs SVC=order for one service)
	docker compose logs -f --tail=100 $(SVC)

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
