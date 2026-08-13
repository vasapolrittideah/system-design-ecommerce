# CLAUDE.md

E-commerce microservices monorepo. **Go · gRPC · PostgreSQL · Kafka · Kong**, hexagonal architecture per service, with a Composition API (BFF) in front.

## Non-negotiable rules

1. **Database per service.** Never query another service's tables. Cross-service reads go through gRPC; cross-service facts arrive via Kafka events.
2. **gRPC is synchronous** (caller needs the answer now). **Kafka is asynchronous** (telling others something happened). Do not use Kafka for request/response, do not use gRPC for fan-out notifications.
3. **Kong handles north-south traffic only.** East-west calls go service-to-service over gRPC. Never route internal calls through the gateway.
4. **Composition API has no database and no business logic.** It fans out, merges, and shapes responses. Nothing else.
5. **`internal/domain` imports only the standard library.** No gRPC, no pgx, no generated proto, no `pkg/`. This is enforced by `depguard` in `.golangci.yml`, not by convention.

## Repository layout

```text
proto/                  # single source of truth for API + event contracts
  ecommerce/common/v1/   ecommerce/<service>/v1/   ecommerce/events/v1/
gen/go/                 # buf generate output — committed to the repo, never hand-edited
pkg/                    # cross-cutting infrastructure — no business logic allowed
services/<name>/        # one service per directory
deploy/kong/kong.yml    # declarative DB-less Kong config
deploy/k8s/
```

Single `go.mod` for the whole repo. Do not introduce per-service modules or a `go.work` unless explicitly asked.

## Service structure (hexagonal)

```text
services/<name>/
├── cmd/
│   ├── server/main.go          # gRPC server
│   ├── worker/main.go          # kafka consumer
│   └── outboxrelay/main.go     # outbox publisher
├── internal/
│   ├── domain/                 # aggregates, invariants, state machines, domain events, domain errors
│   ├── port/
│   │   ├── in/                 # driving ports — use case contracts
│   │   └── out/                # driven ports — repository/gateway/publisher contracts
│   ├── app/                    # use cases implementing port/in; saga orchestration
│   ├── adapter/
│   │   ├── in/{grpc,kafka}/    # proto ⇄ domain mapping only
│   │   └── out/{postgres,kafka,grpcclient}/
│   └── bootstrap/wire.go       # dependency injection
├── db/migrations/
└── Dockerfile
```

Dependency direction: `adapter ──▶ port ◀── app ──▶ domain`, and `domain` depends on nothing.

Where code belongs:

- **Business rules and invariants live in `domain`.** A state transition like `MarkPaid` validates itself and raises a domain event; handlers and use cases never re-implement that check.
- **Use cases orchestrate only** — call gateways, call domain methods, persist, compensate on failure. No `if` statements encoding business policy.
- **Adapters translate.** A gRPC handler maps request → command, calls the use case, maps result → response, and maps errors via `errorx.ToGRPC`. Nothing else.
- Domain aggregates accumulate events internally and expose `PullEvents()`; the repository drains them into the outbox inside the same transaction.

## Service boundaries

A service owns exactly one bounded context: one set of aggregates, one database, one vocabulary. When deciding where new behaviour belongs:

- Ownership follows the data. Whoever owns the table owns the rules that protect it.
- A service must not need another service's internal concepts to enforce its own invariants. If it does, the boundary is drawn wrong.
- Prefer folding new behaviour into an existing service over creating one. Split only when a real pain appears — independent scaling, conflicting release cadence, or a genuinely separate vocabulary.
- Deleting a service is far more expensive than deferring one.

## gRPC

Contract-first with `buf`. Change `.proto` first, run `buf lint`, `buf breaking --against '.git#branch=trunk'`, then `buf generate`. Never edit files under `gen/`.

Request validation is declared in the proto with **protovalidate** and enforced by a single interceptor — do not write per-handler validation code.

Standard server interceptor chain (`pkg/grpcx/server`), in order: recovery → otel → logging → metrics → auth → validate.

Client side (`pkg/grpcx/client`): callers always set a deadline and callees respect `ctx.Done()`; retries only on `Unavailable`/`DeadlineExceeded` with exponential backoff + jitter, and only for idempotent methods; circuit breaker per target service; keepalive plus a default service config for round-robin balancing.

Error mapping lives in `pkg/errorx`:

```text
ErrNotFound                        → codes.NotFound            → 404
ErrInvalidInput                    → codes.InvalidArgument     → 400
ErrInvalidTransition, ErrOutOfStock→ codes.FailedPrecondition  → 409
ErrUnauthorized                    → codes.PermissionDenied    → 403
anything else                      → codes.Internal            → 500 (never leak internals)
```

Attach `ErrorInfo` details (`reason` code + metadata such as `sku`) so clients can handle specific cases.

## PostgreSQL

- `pgx/v5` with `pgxpool`, not `database/sql`.
- `sqlc` for type-safe queries. No ORM.
- Migrations live in `services/<name>/db/migrations/` and run as a Job/init container — never on application startup.
- Every table carries `id uuid`, `created_at`, `updated_at`, `version int` (optimistic locking).
- Transactions go through `pkg/txmanager`: use cases call `tx.Do(ctx, fn)` and stay unaware of PostgreSQL. Repositories pull the tx off the context and fall back to the pool when absent.

**Transactional outbox is mandatory for publishing events.** Write the aggregate and its events to the `outbox` table in one transaction; a relay polls with `FOR UPDATE SKIP LOCKED` and publishes afterwards. Never publish to Kafka directly from a use case.

Outbox rows carry `aggregate_type`, `aggregate_id`, `event_type`, `payload`, `headers` (traceparent, correlation_id), `created_at`, `published_at`, with a partial index on unpublished rows.

## Kafka

Topics: `ecommerce.<domain>.<entity>.<version>`, e.g. `ecommerce.order.events.v1`, with `.dlq` suffix for dead letters.

- Message key = aggregate ID, which gives per-aggregate ordering.
- 6–12 partitions, `replication.factor=3`, `min.insync.replicas=2`, producer `acks=all`.
- Payload schema is protobuf reused from `proto/ecommerce/events/v1`, wrapped in an `EventEnvelope` carrying `event_id`, `event_type`, `aggregate_id`, `version`, `occurred_at`, `correlation_id`, `traceparent`, and an `Any` payload.
- Events are **facts that already happened** (`OrderPaid`), never commands (`SendEmail`). Consumers decide what to do.

Delivery is at-least-once, so **every consumer must be idempotent**: claim the `event_id` in a `processed_events (consumer_group, event_id)` table with `INSERT ... ON CONFLICT DO NOTHING` inside the same transaction as the business work; skip if already claimed.

Commit offsets manually, only after successful processing. Retry three times, then route to the DLQ with an `error_reason` header. Never block a consumer long enough to trigger a rebalance.

Library: `segmentio/kafka-go`.

## Distributed transactions (saga)

Multi-service workflows use **orchestration, not choreography**: the service that owns the aggregate drives the flow, so the whole sequence is readable in one place. Choreography spreads the flow across consumers and is not debuggable.

Shape of an orchestrated flow:

1. The entry call is synchronous and returns as soon as the aggregate is persisted in a pending state — never hold the caller open for the whole workflow.
2. Reserve scarce resources synchronously over gRPC before persisting, so the caller learns about conflicts immediately.
3. Persist the aggregate and its outbox event in one transaction; the rest of the flow proceeds over Kafka.
4. Each downstream step emits a fact, the orchestrator consumes it and advances or compensates the aggregate through its domain state machine.
5. Every forward step has a compensating step, and compensation is idempotent.

Failure handling that must exist in any such flow:

| Risk | Mitigation |
| --- | --- |
| Duplicate client submit | `Idempotency-Key` header → store `(key, request_hash) → response` in Redis for 24h |
| External call times out with unknown outcome | stay pending, reconcile against the provider, accept webhook confirmation |
| Stranded reservation | `expires_at` on the reservation + cron that returns expired capacity |
| Saga stuck mid-flight | `saga_instance` table + timeout worker that forces compensation |
| Overselling / lost update | `UPDATE stock SET available = available - $1 WHERE sku = $2 AND available >= $1` — let the DB be the final guard, never read-then-write |

## Composition API (BFF)

Screen-oriented REST endpoints — one endpoint per screen, not per entity — stateless, no database.

- Separate **critical** from **optional** dependencies. Critical failures fail the request; optional ones degrade with their own child context so they cannot cancel the errgroup.
- Cascading timeout budget: Kong 5s → BFF 800ms → downstream 300ms, leaving room for fallbacks.
- Every service exposes batch `GetXByIDs(ids)` methods. Never loop single-item gRPC calls.
- BFF DTOs are defined separately from service protos so clients never bind to internal structures.
- Short-TTL cache with singleflight for read-heavy data; invalidate via Kafka events.

## Gateway and auth

Kong is declarative and DB-less (`deploy/kong/kong.yml`, version-controlled). It handles TLS termination, JWT signature/expiry verification, rate limiting, CORS, body size limits, correlation ID, and metrics.

Kong must **not** perform business authorization ("does this user own this order?") — that belongs in the owning service. Kong forwards claims via `X-User-ID` / `X-User-Roles`.

Zero-trust: the Composition API re-verifies the JWT itself and never trusts upstream headers alone.

## Observability

- Tracing: OpenTelemetry, propagated through **both gRPC metadata and Kafka headers**, otherwise traces break at every async hop.
- Metrics: Prometheus RED metrics per endpoint, plus consumer lag, saga duration, outbox backlog.
- Logs: `slog` JSON, every line carrying `trace_id`, `correlation_id`, `service`, `user_id`.
- Health: `/healthz` for liveness, `/readyz` checking DB and Kafka.
- Alerts worth having from day one: consumer lag > 10k, any DLQ message, outbox backlog > 1000, p99 over SLO, error rate > 1%.

## Testing

Most tests are domain tests: table-driven, no mocks, millisecond-fast, covering invariants and state machines. Use case tests mock ports with `mockery`. Adapter and integration tests use real PostgreSQL and Kafka via testcontainers — never mock the database driver. Contract testing is `buf breaking` in CI. E2E through Kong stays minimal.

Shared test helpers — container bootstrapping, fixtures, fake clock — live in `pkg/`, not duplicated per service.

## Commands

```text
make proto     # buf lint + buf breaking + buf generate
make migrate   # goose -dir services/$(SVC)/db/migrations postgres "$(DSN)" up
make mock      # mockery --all
make test      # go test ./... -race -cover
make lint      # golangci-lint run
make up        # docker compose up -d --build
```

The local stack runs entirely from `docker compose` — Kafka in KRaft mode (no Zookeeper), Kong DB-less. Every dependency a service needs must be startable this way; nothing may require a shared remote environment to develop against.

## Deployment notes

One Deployment per service with HPA on CPU or consumer lag (KEDA). Migrations run as Jobs/init containers. gRPC needs a headless service with client-side load balancing — an L4 load balancer pins a single connection. Graceful shutdown stops consumers first, then `GracefulStop()`, with `terminationGracePeriodSeconds: 30`. Config comes from env (12-factor); secrets from External Secrets / Sealed Secrets.

## Working conventions

- Adding or changing an API: edit the proto, regenerate, then update handler ↔ domain mapping. Never work backwards from generated code.
- Adding a service: copy the layering, wiring, and test structure of an existing service rather than inventing a new arrangement. Consistency across services matters more than local elegance.
- Anything cross-cutting goes in `pkg/` and must stay free of business logic; anything business-specific stays inside its service.
- Commits follow Conventional Commits.
