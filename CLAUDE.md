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

Standard server interceptor chain (`pkg/grpcx/server`), in order: recovery → logging → metrics → auth → validate. Tracing is not in that list because otel is installed as a `stats.Handler`, its interceptor form being deprecated upstream — which wraps the whole chain rather than sitting inside it, so the span exists before recovery runs and a panic lands on the trace instead of beside it.

Consequences of that order, both deliberate:

- Recovery is outermost, so it catches a panic thrown by any other interceptor. It runs before logging has put a request logger in the context, so panic lines carry `trace_id` but not `correlation_id`.
- Auth is inside logging, so the access line has no `user_id`. Handler logs do; join them by `trace_id`.

The chain also owns the correlation ID: it adopts an inbound `x-correlation-id` or mints one, puts it in the context, and echoes it as a response header.

`pkg/grpcx` itself only holds what both ends must agree on — the identity metadata keys and `Identity` in the context. Authentication establishes *who* is calling and nothing more; whether that caller may touch this aggregate is a business rule owned by the service. The default authenticator reads the identity forwarded from the edge and never rejects, because relays, timeout workers, and plain service-to-service reads legitimately arrive with no user behind them. Services that are themselves the first reachable hop — the Composition API — pass their own verifier via `WithAuth`.

Client side (`pkg/grpcx/client`): callers always set a deadline and callees respect `ctx.Done()` (a call arriving without a deadline gets a default one rather than waiting forever); retries only on `Unavailable`/`DeadlineExceeded` with exponential backoff + jitter, and only for idempotent methods; circuit breaker per target service, outside the retry loop so one logical call counts once; keepalive plus a default service config for round-robin balancing.

Two rules the client depends on:

- **Method names decide retries.** `Get*`, `List*`, `Batch*`, `Search*`, `Count*`, `Check*` are treated as idempotent; anything else is not. A method named like a read that is not one — `GetOrCreateCart` — will be retried wrongly. Name mutations for the mutation, or list the exceptions in `IDEMPOTENT_METHODS`.
- **Only failures that mean the target is unhealthy count against the breaker** — `Unavailable`, `DeadlineExceeded`, `ResourceExhausted`, `Internal`, `Unknown`, `DataLoss`. `NotFound` and `FailedPrecondition` are the service working correctly; counting them would let a run of sold-out SKUs trip the breaker and take checkout down.

East-west traffic is plaintext. TLS is terminated by Kong at the edge, and internal encryption, when it is wanted, comes from the mesh rather than from every service growing its own certificate handling.

Error mapping lives in `pkg/errorx`, and every error resolves to exactly one `Kind`:

```text
KindNotFound      → codes.NotFound            → 404
KindInvalidInput  → codes.InvalidArgument     → 400
KindConflict      → codes.FailedPrecondition  → 409
KindUnauthorized  → codes.PermissionDenied    → 403
KindInternal      → codes.Internal            → 500 (never leak internals)
```

The kinds are transport-shaped, never business-shaped: `KindConflict`, not `ErrOutOfStock`. A service names its own failures in its own vocabulary and points them at a kind — `pkg/` may not learn what a SKU is.

**A domain package declares its kind structurally, never by importing `errorx`.** It implements `ErrorKind() string` returning one of the kind strings (`"conflict"`, `"not_found"`, …), which is a method signature and therefore no dependency at all — the same trick as `Unwrap` and `Stringer`. This is what keeps `internal/domain` stdlib-only while its errors still map correctly. Code outside `domain` builds errors directly instead: `errorx.New(errorx.KindNotFound, "order %s not found", id)`, or `errorx.Wrap` where a lower layer's failure is the explanation. An error declaring no kind is Internal — an unclassified failure is a bug until someone classifies it, and defaulting the other way would answer 400 for a broken database.

`ToGRPC` resolves in a fixed order, each step existing because the next would get that case wrong:

1. **A declared kind** wins over everything, so a use case can reclassify what a lower layer said.
2. **An error that is already a gRPC status keeps its code.** Gateways hold these; flattening a downstream `Unavailable` into `Internal` hides from the caller's circuit breaker exactly what it exists to detect.
3. **A cancelled or expired context** becomes `Canceled`/`DeadlineExceeded`. These arrive bare from pgx and anything selecting on `ctx.Done()`, and reporting a caller who hung up as `Internal` puts client behaviour into the error rate and into breakers that should not have been touched.
4. Anything else is `Internal`.

Only the `Internal` message is replaced — every other kind is a fact the caller asked for. The returned error still wraps the original, so the access line logs the full chain while the client gets the scrubbed status; without that, hiding internals would also erase the only record of what broke.

Attach `ErrorInfo` details so clients can handle specific cases — `.WithReason("OUT_OF_STOCK").WithMetadata(map[string]string{"sku": sku})`. Reason codes are API: a client branches on them, so renaming one is a breaking change. Every error carries one whether or not it was set, defaulted from the kind, so nobody has to pattern-match a message. Reading back on the other side is `errorx.Reason` / `errorx.Metadata`, and the Composition API turns a status into an HTTP code with `errorx.HTTPStatus`.

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

**`observability.Start` runs first in `main`, before anything else is constructed.** otelgrpc resolves `otel.GetTracerProvider()` at the moment its handler is built, so a process that wires its gRPC server before its telemetry captures the no-op provider permanently: no span is ever exported, `trace_id` is empty on every log line, and nothing reports an error. This is the one ordering rule in the repo that fails silently.

```go
obs := observability.MustStart(ctx, obsCfg, observability.WithLogger(log))
defer obs.Shutdown(context.WithoutCancel(ctx))

srv := server.MustNew(grpcCfg, server.WithLogger(log), server.WithRegisterer(obs.Registry()))
obs.AddReadinessCheck("postgres", pool.Ping)
```

- Tracing: OpenTelemetry over OTLP/gRPC to Jaeger/Tempo, propagated through **both gRPC metadata and Kafka headers** (W3C trace context + baggage), otherwise traces break at every async hop. Sampling is **parent-based** — sampling per service independently is how traces come back with the middle missing — so `SAMPLE_RATIO` only governs traces this process itself begins.
- Metrics: Prometheus RED metrics per endpoint, plus consumer lag, saga duration, outbox backlog. The registry is owned by `pkg/observability` and passed down explicitly; nothing reaches for `prometheus.DefaultRegisterer`. The gRPC set comes from `pkg/grpcx/server` and no handler emits its own: `grpc_server_handled_total` (with `grpc_code`), `grpc_server_handling_seconds` (deliberately without it — a 13-bucket histogram multiplied by every status code is how a metrics backend drowns), `grpc_server_in_flight_requests`, `grpc_server_panics_recovered_total`. otelgrpc's own `rpc.*` metrics are suppressed so the same calls are not measured twice under two naming schemes.
- Logs: `zap` JSON on stdout via `pkg/logger` — never a log file, never a second logging library. Request-scoped code takes its logger from the context with `logger.From(ctx)`, which attaches `trace_id`, `span_id`, `correlation_id`, and `user_id`; `service` and `version` are bound at construction. Store the plain logger with `logger.Into(ctx, log)` — never the result of `From`, or the context fields duplicate on the next hop.
- Log level follows meaning, not status: `NotFound`, `FailedPrecondition`, and the rest of the expected outcomes log at info. Only codes that say the service is broken log at error, or the error rate measures user behaviour instead of health and the alert on it never stops firing.
- Admin endpoints live on their own port (`OBS_ADMIN_ADDR`, default `:9090`), never routed to from outside the cluster: `/metrics`, `/healthz`, `/readyz`. **Liveness checks nothing** — a liveness failure restarts the pod, and restarting because a database is unreachable turns one outage into a crash-loop across every replica. Dependencies go in readiness via `AddReadinessCheck`, registered as each one is wired; they run in parallel under one shared budget and the response body names what failed.
- gRPC servers additionally serve the standard gRPC health service, and flip it to `NOT_SERVING` before draining so callers stop routing here while in-flight calls finish.
- Alerts worth having from day one: consumer lag > 10k, any DLQ message, outbox backlog > 1000, p99 over SLO, error rate > 1%, any recovered panic.

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

One Deployment per service with HPA on CPU or consumer lag (KEDA). Migrations run as Jobs/init containers. gRPC needs a headless service with client-side load balancing — an L4 load balancer pins a single connection. Config comes from env (12-factor); secrets from External Secrets / Sealed Secrets.

Two couplings between settings that are easy to break by tuning one side alone:

- Graceful shutdown stops consumers first, then hands the gRPC server its cancelled context; `pkg/grpcx/server` drains within `SHUTDOWN_TIMEOUT` (25s) and forces a stop after. That has to stay under `terminationGracePeriodSeconds: 30`, or the kubelet's SIGKILL lands mid-drain and the graceful path never runs.
- Client-side balancing only spreads across replicas that existed when the caller last resolved. `MAX_CONNECTION_AGE` on the server is what forces callers to re-resolve, so scaling out actually receives traffic. A client's `KEEPALIVE_TIME` must also stay above the server's `MIN_CLIENT_PING_INTERVAL`, or the server answers a well-behaved caller's pings with GOAWAY.

Every component that needs configuration declares its own struct with `env` tags and loads it through `pkg/config` — `config.MustLoad[T](config.WithPrefix("..."))` in `main.go` or `bootstrap`, then passed down explicitly. Nothing calls `os.Getenv` at runtime, and no code branches on an environment name: differences between deploys live in the values, not in `if env == "production"`.

## Working conventions

- Adding or changing an API: edit the proto, regenerate, then update handler ↔ domain mapping. Never work backwards from generated code.
- Adding a service: copy the layering, wiring, and test structure of an existing service rather than inventing a new arrangement. Consistency across services matters more than local elegance.
- Anything cross-cutting goes in `pkg/` and must stay free of business logic; anything business-specific stays inside its service.
- Commits follow Conventional Commits.
