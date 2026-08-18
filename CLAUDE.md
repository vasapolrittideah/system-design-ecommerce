# CLAUDE.md

E-commerce microservices monorepo. **Go · gRPC · PostgreSQL · Kafka · Kong**, hexagonal architecture per service, with a BFF in front — one per audience, `bff-web` today.

## Non-negotiable rules

1. **Database per service.** Never query another service's tables. Cross-service reads go through gRPC; cross-service facts arrive via Kafka events.
2. **gRPC is synchronous** (caller needs the answer now). **Kafka is asynchronous** (telling others something happened). Do not use Kafka for request/response, do not use gRPC for fan-out notifications.
3. **Kong handles north-south traffic only.** East-west calls go service-to-service over gRPC. Never route internal calls through the gateway.
4. **A BFF has no database and no business logic.** It fans out, merges, and shapes responses. Nothing else.
5. **`internal/domain` imports libraries, never layers, and shares no code with another service.** The allow-list is the standard library plus `google/uuid`, and that is the whole of it: no `pkg/`, no generated proto, no gRPC, pgx, or Kafka. Imports pointing *inward* need no rule — port, app, and adapter all import domain, so an import back at any of them is a cycle that does not build; `depguard` in `.golangci.yml` exists for the outward ones, which compile fine and are what hexagonal actually forbids. The line is between a library and a layer rather than between third-party and first-party code: a library carries no domain meaning and nobody here can change it, while `pkg/` is denied even where one package looks harmless, because a package we own can grow — today's UUID helper is next quarter's shared `Money`, and by then two services import it and changing it for one changes it for both. Where a library does not already exist the code is copied per service, which is the right trade wherever divergence would harm nobody; where divergence would instead be a *bug*, the answer is a deliberate shared kernel with an owner, argued as an architecture decision, never one more entry in the allow-list.

## Repository layout

```text
proto/                  # single source of truth for API + event contracts
  ecommerce/common/v1/   ecommerce/<service>/v1/   ecommerce/events/v1/
gen/go/                 # buf generate output — committed to the repo, never hand-edited
docs/proto/             # buf generate output too: the service reference, rendered from the .proto comments
pkg/                    # cross-cutting infrastructure — no business logic allowed
services/<name>/        # one service per directory
deploy/k8s/
  infra/kong/kong.yml    # declarative DB-less Kong config
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

Request validation is declared in the proto with **protovalidate** and enforced by a single interceptor — do not write per-handler validation code. A BFF is the one place that cannot use it, because its request bodies are hand-written DTOs rather than protos; those are validated by `validate` struct tags — through `httpx.Validator.Bind` for a body and `httpx.Validator.BindQuery` for a query string, which is the same mechanism reached from the other half of the request. Those are the only two mechanisms in the repo. A handler that checks its own input by hand is a bug wherever it appears.

Standard server interceptor chain (`pkg/grpcx/server`), in order: recovery → logging → metrics → auth → validate. Tracing is not in that list because otel is installed as a `stats.Handler`, its interceptor form being deprecated upstream — which wraps the whole chain rather than sitting inside it, so the span exists before recovery runs and a panic lands on the trace instead of beside it.

Consequences of that order, both deliberate:

- Recovery is outermost, so it catches a panic thrown by any other interceptor. It runs before logging has put a request logger in the context, so panic lines carry `trace_id` but not `correlation_id`.
- Auth is inside logging, so the access line has no `user_id`. Handler logs do; join them by `trace_id`.

The chain also owns the correlation ID: it adopts an inbound `x-correlation-id` or mints one, puts it in the context, and echoes it as a response header.

`pkg/grpcx` itself only holds what both ends must agree on — the identity metadata keys and `Identity` in the context. Authentication establishes *who* is calling and nothing more; whether that caller may touch this aggregate is a business rule owned by the service. The default authenticator reads the identity forwarded from the edge and never rejects, because relays, timeout workers, and plain service-to-service reads legitimately arrive with no user behind them. Verifying a token is `pkg/auth`'s job, and it happens at the edge — a BFF's HTTP middleware — not in this chain. `WithAuth` exists for a gRPC server that ever becomes a first reachable hop; nothing is one today, and a service that grows into one wraps `pkg/auth`'s verifier rather than writing its own.

Client side (`pkg/grpcx/client`): callers always set a deadline and callees respect `ctx.Done()` (a call arriving without a deadline gets a default one rather than waiting forever); retries only on `Unavailable`/`DeadlineExceeded` with exponential backoff + jitter, and only for idempotent methods; circuit breaker per target service, outside the retry loop so one logical call counts once; keepalive plus a default service config for round-robin balancing.

Two rules the client depends on:

- **Method names decide retries.** `Get*`, `List*`, `Batch*`, `Search*`, `Count*`, `Check*` are treated as idempotent; anything else is not. A method named like a read that is not one — `GetOrCreateCart` — will be retried wrongly. Name mutations for the mutation, or list the exceptions in `IDEMPOTENT_METHODS`.
- **Only failures that mean the target is unhealthy count against the breaker** — `Unavailable`, `DeadlineExceeded`, `ResourceExhausted`, `Internal`, `Unknown`, `DataLoss`. `NotFound` and `FailedPrecondition` are the service working correctly; counting them would let a run of sold-out SKUs trip the breaker and take checkout down.

East-west traffic is plaintext. TLS is terminated by Kong at the edge, and internal encryption, when it is wanted, comes from the mesh rather than from every service growing its own certificate handling.

Error mapping lives in `pkg/errorx`, and every error resolves to exactly one `Kind`:

```text
KindNotFound         → codes.NotFound            → 404
KindInvalidInput     → codes.InvalidArgument     → 400
KindConflict         → codes.FailedPrecondition  → 409
KindUnauthenticated  → codes.Unauthenticated     → 401
KindUnauthorized     → codes.PermissionDenied    → 403
KindInternal         → codes.Internal            → 500 (never leak internals)
```

The kinds are transport-shaped, never business-shaped: `KindConflict`, not `ErrOutOfStock`. A service names its own failures in its own vocabulary and points them at a kind — `pkg/` may not learn what a SKU is.

`KindUnauthenticated` and `KindUnauthorized` are never interchangeable, and only the second one is a service's to raise. 401 says the credential is the problem, so the client should run its refresh flow; 403 says the credential was fine and the answer is still no. A frontend handed 403 for an expired token logs the user out instead of quietly renewing. Only a process that verifies tokens itself produces the first, and that is the BFF alone. A service reached over east-west gRPC has had identity settled a hop earlier and only ever answers the authorization question.

**A domain package declares its kind structurally, never by importing `errorx`.** It implements `ErrorKind() string` returning one of the kind strings (`"conflict"`, `"not_found"`, …), which is a method signature and therefore no dependency at all — the same trick as `Unwrap` and `Stringer`. This is what keeps `internal/domain` free of `pkg/` while its errors still map correctly. Code outside `domain` builds errors directly instead: `errorx.New(errorx.KindNotFound, "order %s not found", id)`, or `errorx.Wrap` where a lower layer's failure is the explanation. An error declaring no kind is Internal — an unclassified failure is a bug until someone classifies it, and defaulting the other way would answer 400 for a broken database.

`ToGRPC` resolves in a fixed order, each step existing because the next would get that case wrong:

1. **A declared kind** wins over everything, so a use case can reclassify what a lower layer said.
2. **An error that is already a gRPC status keeps its code.** Gateways hold these; flattening a downstream `Unavailable` into `Internal` hides from the caller's circuit breaker exactly what it exists to detect.
3. **A cancelled or expired context** becomes `Canceled`/`DeadlineExceeded`. These arrive bare from pgx and anything selecting on `ctx.Done()`, and reporting a caller who hung up as `Internal` puts client behaviour into the error rate and into breakers that should not have been touched.
4. Anything else is `Internal`.

Only the `Internal` message is replaced — every other kind is a fact the caller asked for. The returned error still wraps the original, so the access line logs the full chain while the client gets the scrubbed status; without that, hiding internals would also erase the only record of what broke.

Attach `ErrorInfo` details so clients can handle specific cases — `.WithReason("OUT_OF_STOCK").WithMetadata(map[string]string{"sku": sku})`. Reason codes are API: a client branches on them, so renaming one is a breaking change. Every error carries one whether or not it was set, defaulted from the kind, so nobody has to pattern-match a message. Reading back on the other side is `errorx.Reason` / `errorx.Metadata`, and a BFF turns a status into an HTTP code with `errorx.HTTPStatus`.

## PostgreSQL

- `pgx/v5` with `pgxpool`, not `database/sql`.
- `sqlc` for type-safe queries. No ORM.
- Migrations live in `services/<name>/db/migrations/` and run as a Job/init container — never on application startup. `sqlc` reads that same directory as its schema source, so the migration files *are* the schema; there is no second `schema.sql` to keep in step.
- Every table carries `id uuid`, `created_at`, `updated_at`, `version int` (optimistic locking).
- Transactions go through `pkg/txmanager`: use cases call `tx.Do(ctx, fn)` and stay unaware of PostgreSQL. Repositories pull the tx off the context and fall back to the pool when absent.

The migration tool is **goose**, picked over golang-migrate for how each one fails rather than how it runs. golang-migrate marks `schema_migrations.dirty` when a migration fails and refuses every later run until someone connects and forces a version — inside an init container that is a crash-loop on every replica, waiting on a human, for a failure that may have been a lock timeout. goose runs each migration in a transaction and records nothing when it rolls back, so a transient failure clears itself on the next restart. Neither survives a half-applied `-- +goose NO TRANSACTION` migration, which is the price of `CREATE INDEX CONCURRENTLY`.

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

## BFF

Screen-oriented REST endpoints — one endpoint per screen, not per entity — stateless, no database.

**One BFF per audience, named `bff-<audience>`.** `bff-web` serves the storefront; an admin console arrives as `bff-admin`, not as a route group inside this one. Screens belong to an audience, so a single BFF serving two would hold two unrelated sets of endpoints and two unrelated authorization stories in one process. The prefix is also what `.golangci.yml` matches to let a BFF import `pkg/httpx` while every other service is denied it — a new BFF named to the pattern needs no lint change, and a service that grows an HTTP handler is either the wrong place for it or a BFF that was not named like one.

Its structure is the hexagonal layout with the layers it has no use for left out: `cmd/server`, `internal/bootstrap`, `internal/adapter/in/rest`, and nothing else. There is no `domain`, no `port`, and no `app`, because there are no rules to protect and nothing to invert — the handler depends on the generated client interface directly, since the proto already is the contract and a hand-written mirror of it would be a second copy to keep in step. An `app` package earns its place the day a use case fans out across several services and has to name something the generated code does not.

- Separate **critical** from **optional** dependencies. Critical failures fail the request; optional ones degrade with their own child context so they cannot cancel the errgroup.
- Cascading timeout budget: Kong 5s → BFF 800ms → downstream 300ms, leaving room for fallbacks. The BFF's share is one `httpx.WithRequestTimeout`, and it reaches every fan-out call as a context deadline `pkg/grpcx/client` inherits — no handler passes it along.
- Every service exposes batch `GetXByIDs(ids)` methods. Never loop single-item gRPC calls.
- BFF DTOs are defined separately from service protos so clients never bind to internal structures.
- Short-TTL cache with singleflight for read-heavy data; invalidate via Kafka events.
- **A BFF registers no readiness check for the services it calls.** An unreachable dependency is a 503 with a reason code, which is a better answer than every replica leaving the endpoint list at once — gating on it would turn a downstream service's routine rollout into an outage with nothing left to route to.

The HTTP side is `pkg/httpx`, which services never import — they speak gRPC, and `pkg/errorx` already carries their errors this far. Routers come from `httpx.MustNewRouter`, never a bare `chi.NewRouter`: chi answers an unrouted path, a wrong method, and a panic in `text/plain`, and its recoverer prints the stack into the response, so three exceptions to the response contract exist from the first commit unless its fallbacks are replaced.

That router's chain is correlation → logging → metrics → recovery → localize → timeout, which is the reverse of `pkg/grpcx/server` on the one point where they differ. There recovery is outermost because the correlation ID is minted by the logging interceptor; here it arrives as a header, so recovery can sit *inside* logging and metrics — and has to, or a panicked request unwinds past both and is never counted as the 500 it became. The server wraps the whole chain in otel the same way otelgrpc does, so a panic lands on the trace rather than beside it.

**The BFF is where a trace begins.** Sampling is parent-based everywhere else, so `OBS_SAMPLE_RATIO` here decides how many traces the whole system has, and `observability.Start` running before `httpx.NewServer` is what makes them exist at all.

**`httpx.CorrelationID` is mandatory, and omitting it fails silently.** It is where the ID enters the system: `pkg/grpcx/client` reads it off the context and forwards it to every service the request fans out to, and those services put it on the outbox rows for the events they raise. Without the middleware the context holds nothing, every downstream service mints its own ID, and one user-visible operation appears in the logs as several unrelated ones — with no error anywhere.

**Every failure the application decided on is one shape**, `httpx.ErrorResponse`, and JSON is **camelCase** throughout. Clients branch on `error.code` — the reason code from `errorx` — never on the HTTP status: 409 alone cannot say whether an order was already paid or a SKU was sold out. Renaming a reason code is a breaking change.

The qualifier is load-bearing, because three answers come from the gateway and cannot carry that shape: Kong short-circuits them before any plugin could rewrite the body, which is why the plugin that would is Enterprise. They are JSON — `error_default_type` sees to that — but they carry `message` and no `error.code`:

| Status | When | Why it cannot move behind Kong |
| --- | --- | --- |
| 429 | rate limit | limiting at the edge is the point; a limiter the BFF runs has already accepted the request |
| 413 | body over Kong's hard cap | Kong's cap is 8× `httpx.MaxBodyBytes` on purpose, so a merely-oversized body is refused by the BFF as `BODY_TOO_LARGE` and only an absurd one lands here |
| 502/503/504 | bff-web unreachable or too slow | nothing behind Kong is left to answer |

Everything else that used to belong on this list was moved rather than accepted: 404 and 405 because Kong routes `/` as a catch-all and lets `httpx` answer, 401 because Kong no longer verifies tokens. A client needs one rule for the remainder — *no `error.code` means derive it from the status* (429 → `RATE_LIMITED`, 5xx → `UPSTREAM_UNAVAILABLE`) — which it needs regardless, since a CDN or load balancer above Kong answers in its own shape too.

**Read the correlation ID from the `X-Correlation-ID` response header, not from `error.correlationId`.** Kong echoes the header on every answer including its own; the body field exists only when the BFF wrote the body. One source works for both.

Responses are localised from `Accept-Language`, defaulting to `en`, with the negotiated language echoed as `Content-Language`. Only `error.fields[].message` is translated. `error.message` is a developer aid and a last-resort string — never render it to a user, or a Thai screen shows English prose the moment anything but validation fails.

## Gateway and auth

Kong is declarative and DB-less (`deploy/k8s/infra/kong/kong.yml`, version-controlled — it sits with Jaeger and Reloader because nothing here builds its image). It handles TLS termination, rate limiting, CORS, body size limits, correlation ID, and metrics.

**Kong does not verify JWTs, and no plugin forwards claims.** Authentication happens once, in the BFF, which re-verifies every token itself under zero-trust — so a verifier at the edge would be a second copy of the public key to rotate, a second `iss`/`aud` policy to keep in step, and a 401 in Kong's error shape rather than this API's. `pkg/auth`'s middleware ignores `X-User-ID` / `X-User-Roles` anyway, so a forwarded claim would have no reader. Kong's `jwt` plugin could not do the job as specified regardless: it checks `exp` and `nbf` and never `aud`, and it forwards `X-Consumer-*` rather than claims.

What is deliberately left out, each for a reason that is easy to undo by accident:

- **No `opentelemetry` plugin.** The BFF is where a trace begins; a span started at this hop makes Kong the root and silently takes the sampling decision away from `OBS_SAMPLE_RATIO`.
- **No route per endpoint.** One catch-all route on `/` sends everything to bff-web, so adding an endpoint touches one repo and a mistyped path is answered by `httpx`'s own `ROUTE_NOT_FOUND` instead of Kong's "no Route matched". Longest prefix wins, so a route that exists only to carry a tighter rate limit — `/api/v1/auth/login`, `/api/v1/auth/register` — still takes precedence.
- **`retries: 0`.** `pkg/grpcx/client` already retries what is safe to; a gateway retrying on top turns one slow `POST /auth/register` into two accounts.
- **The upstream is a ring balancer over a headless Service, not the Service itself.** Kong holds keep-alive connections, so a cluster IP pins every request to whichever pod answered first and a replica the HPA adds receives nothing.

Kong must **not** perform business authorization ("does this user own this order?") — that belongs in the owning service.

Tokens are **ES256**, and the asymmetry is what makes zero-trust affordable: the identity service holds the private key and is the only thing that can mint a token; every BFF holds a public key that can only ever say no. A shared secret would put the credential that forges any user's identity inside the process most exposed to the outside. ES256 over RS256 for size — a P-256 key is 32 bytes against 256, on a header that rides every request.

`pkg/auth` is the one place a token becomes an identity. It owns the `Claims` shape both ends must agree on, the `Signer` (identity only), the `Verifier`, and the `Authenticate` HTTP middleware that puts a `grpcx.Identity` on the request context — where `pkg/grpcx/client` picks it up and forwards it to every hop. It owns nothing else: TTL policy, role assignment, and refresh-token storage are the identity service's, and authorization belongs to whoever owns the aggregate.

Rules that package settles once:

- **The verifier pins `ES256` rather than reading the token's `alg` header.** A parser that believes the token about how to check the token accepts `alg: none`, and accepts the public key replayed as an HMAC secret.
- **`iss` and `aud` are checked, not merely read.** Otherwise a staging token opens a production session wherever a key pair is shared.
- **The middleware ignores `X-User-ID` / `X-User-Roles` entirely.** Identity comes from the signature it verified itself; that is the whole content of the zero-trust rule, and it is what makes a client setting those headers by hand a non-event.
- **Rejections carry `TOKEN_MISSING`, `TOKEN_EXPIRED`, or `TOKEN_INVALID`** and nothing finer. `TOKEN_EXPIRED` is the only one a client acts on differently — refresh rather than log in again — and collapsing the rest means someone probing signatures learns nothing from the answer.
- **`kid` is stamped on every token from the first one**, though there is one key and the verifier ignores it. Rotating a key means running two while the old tokens drain, and a verifier can only pick between them if the token says which signed it — retrofitting that would invalidate every token already in a browser.
- **Access tokens are JWTs; refresh tokens are not.** A refresh token has to be revocable, which a self-contained signed token cannot be — logout would have to wait out the TTL. Refresh tokens are opaque random strings whose hash the identity service stores in a table, where a row can be deleted.
- **Access-token TTL stays short** (15m default), because until it expires a stolen token works, a deleted user is still logged in, and a revoked role is still held.

PEM keys reach a process through env as either the PEM itself, which a Kubernetes Secret carries fine, or base64 of it, which is the shape that survives any delivery keeping a value on one line. `pkg/auth` accepts both by looking at the value.

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
- **Latency histograms carry the trace ID of a sampled request as an exemplar**, which is what turns a point on a p99 panel into the trace behind it. Three things have to agree or the link silently does not exist, and none of them reports its absence: the process attaches it (`pkg/grpcx/server`, `pkg/httpx` — only for sampled spans, since an unsampled trace was never exported and the link would lead nowhere), `pkg/observability` serves `/metrics` with `EnableOpenMetrics` (exemplars exist in no other exposition format), and Prometheus runs with `--enable-feature=exemplar-storage`. The middle one is the easiest to lose: it looks like a formatting preference.
- Grafana lives in `deploy/k8s/infra/grafana`, and **its datasources and dashboards are files, not clicks** — provisioned from ConfigMaps with `allowUiUpdates: false`. A dashboard built in the UI lives in a pod's sqlite: not reviewable, not in git, and gone at the next restart, which Reloader performs on every config change. Editing a panel means editing its JSON, the same path `kong.yml` takes. The datasources point at each other in both directions — Prometheus exemplars link into Jaeger, Jaeger's `tracesToMetrics` links back — which works because `service` names the same thing on a span and on a series.
- Prometheus lives in `deploy/k8s/infra/prometheus` and discovers targets **by container port name, not by annotation**: a pod is scraped because a port of its is called `admin` (Kong's is `status`). A new service is therefore scraped for following the layout, and one that renames the port disappears from every dashboard and alert with nothing reporting it. Discovery is namespace-scoped and the RBAC grant matches, so a scrape config widened by accident fails with a 403 rather than quietly collecting the cluster.
- Alerts worth having from day one, in `deploy/k8s/infra/prometheus/alerts.yml`: consumer lag > 10k, any DLQ message, outbox backlog > 1000, p99 over SLO, error rate > 1%, any recovered panic. An alert fires on a metric something here actually emits — a rule against a metric nobody publishes never fires and reads as coverage that does not exist — so the Kafka ones arrive with Kafka. Error-rate rules count only the codes that mean the service is broken, the same list the client's circuit breaker uses; a rate that counts `NotFound` measures user behaviour and gets silenced within a week. There is no Alertmanager locally: rules evaluate and show under `/alerts`, and routing is a production concern.

## Testing

Most tests are domain tests: table-driven, no mocks, millisecond-fast, covering invariants and state machines. Use case tests mock ports with `mockery`. Adapter and integration tests use real PostgreSQL and Kafka via testcontainers — never mock the database driver. Contract testing is `buf breaking` in CI. E2E through Kong stays minimal.

Shared test helpers — container bootstrapping, fixtures, fake clock — live in `pkg/`, not duplicated per service.

**Load testing is k6 through the gateway** — `scripts/load`, run by `make load`, deliberately manual and never a CI gate. It offers a fixed arrival rate rather than holding a fixed number of virtual users, so a system that slows down builds a queue instead of quietly receiving less traffic, which is the behaviour worth watching. Two things about it are load-bearing:

- **The run raises Kong's rate limits and restores them afterwards**, deriving the raised config from `kong.yml` itself rather than keeping a second copy. 300 requests a minute is a limit set for people, and a run against it measures the limiter and nothing behind it. The bypass is deliberately not a route or a header that skips the plugin — that is a rate limit anyone can opt out of, and it would be one merge away from being true in production.
- **The numbers are not a capacity result.** Generator, cluster, and every database share one laptop, and the local overlays request 10m of CPU per pod so that the HPA is reachable at all. What transfers is the shape: which limit is met first, which error the system answers with, and whether scaling out changed it.

## Commands

```text
make proto              # buf lint + buf breaking + buf generate
make migrate-up SVC=x   # goose -dir services/x/db/migrations postgres "$(DSN)" up
make mock               # mockery, driven by .mockery.yml
make test               # go test ./... -race -cover
make lint               # golangci-lint run
make cluster-create     # k3d cluster with its registry, once
make dev                # tilt up — watch, rebuild, redeploy
make up                 # apply deploy/k8s/infra into the cluster
make deploy SVC=x       # build image, run the migration Job, roll out
make load SCENARIO=me   # k6 through Kong, with its rate limits raised for the run
```

The local stack is a **k3d cluster**, not docker compose: `deploy/k8s/infra` for the dependencies every service shares (Jaeger, Kong DB-less, Reloader; Kafka in KRaft mode as the phase needing it arrives) and `deploy/k8s/base/<name>` + `deploy/k8s/overlays/local/<name>` for the services.

**Databases are not shared infrastructure.** Each service's local overlay declares its own single-database Postgres instance, so "database per service" is structural rather than a matter of grants. One instance holding a database per service is cheaper and was what this stack ran first, but Postgres grants `CONNECT` on every new database to `PUBLIC`, so each service role could open a connection to every other service's database and list its tables through `pg_catalog` — closing that needed a `REVOKE` in an init script whose absence nothing would report. With an instance per service there is nothing to revoke, and reaching another service's data would mean reaching another Service. Every dependency a service needs must be startable this way; nothing may require a shared remote environment to develop against.

**The namespace denies ingress by default, and each workload carries the allow-list of who may reach it.** `deploy/k8s/infra/namespace.yaml` holds the deny — with the Namespace rather than in the infra kustomization, because `make down` removes that kustomization while the services it was protecting keep running. Everything else sits beside the thing it protects: a service's in `base/<name>/networkpolicy.yaml`, a database's in the overlay that declares the database, an infra component's next to its Deployment.

This is what makes two rules above manifests instead of conventions. "Never query another service's tables" is enforced from the side that owns them — `identity-postgres` accepts connections from pods labelled `identity` and from nothing else, so a second service reaching for it fails to connect rather than reading rows. "East-west calls never go through the gateway" is a rule about who may *call*, so it lives on the other side: each service's egress list names DNS, Jaeger, its own database, and the services it calls, and Kong is absent from every one of them.

Three things about it are easy to get wrong:

- **Ports are numeric, not the port names the Services and the scrape config use.** A NetworkPolicy may name a port, but resolving it is the CNI's to implement, and a rule the CNI ignores is a rule that is not there.
- **Egress is denied only on the service workloads**, never namespace-wide. Prometheus's pod discovery and Reloader's watch both reach the API server, and allowing that means writing a ClusterIP into a manifest — a fragile rule in service of no architectural rule.
- **A new service that ships no policy is unreachable**, and the first sign is `make deploy` failing its smoke test. That is the better of the two failures; the other default is a service reachable by everything in the namespace, which nothing ever reports.

Probes and `make port-forward` arrive from the node rather than from a pod and are not filtered, so neither needs a rule.

Kubernetes locally rather than compose because the rules this repo cares about most are the ones compose cannot express: a headless Service with client-side gRPC balancing, `MAX_CONNECTION_AGE` forcing callers to re-resolve, `SHUTDOWN_TIMEOUT` fitting inside `terminationGracePeriodSeconds`, migrations as a Job, and readiness removing a pod from the endpoint list. Manifests that are never run before production are three bugs discovered on the same afternoon.

The cost is the inner loop, and `make dev` is the whole answer to it: Tilt watches the tree and rebuilds and redeploys into the cluster — about eleven seconds from a Go edit to the new binary serving. Editing any file the image is built from is enough; nothing else needs running.

**There is deliberately no second, faster loop that runs a service on the host.** One was half-built and removed. Everything a service reads comes from a ConfigMap and a Secret that kustomize assembles and the kubelet injects, so a host-run process has to reproduce that environment by hand — with values that are not merely absent but different, since `IDENTITY_DB_HOST` is a Service name in the cluster and `localhost` behind a port-forward. That hand-written copy drifts silently the moment an overlay changes, and the loop it buys is worth a few seconds against a rebuild already measured in eleven. A tool like `air` would only automate restarting that process; it does not address the environment, which is the part that actually costs. `make port-forward` stays, because goose and psql need a database on `localhost` — not because a service is meant to run there.

**The Tiltfile deliberately has no live update.** Syncing a host-compiled binary into the running container would take that eleven seconds to two or three, and costs four divergences from production to do it: the Go sources have to leave the image build context or every edit invalidates it and the sync never fires, the server has to run a different distroless variant because the restart wrapper needs a `touch` the minimal image lacks, `readOnlyRootFilesystem` has to be off, and the binary gets compiled twice. That is a poor trade in a repo whose reason for running Kubernetes locally is not having such divergences. Re-measure before revisiting it.

Two manifests carry couplings that break silently when one side is tuned alone. `terminationGracePeriodSeconds` (35s) must exceed the `preStop` sleep (5s) plus `IDENTITY_GRPC_SHUTDOWN_TIMEOUT` (25s). And the Deployment sets no CPU limit on purpose: the HPA measures utilisation against the CPU *request*, so a throttled pod reports spare capacity and the autoscaler declines to scale exactly when it should.

## Deployment notes

One Deployment per service with HPA on CPU or consumer lag (KEDA). Migrations run as Jobs/init containers. gRPC needs a headless service with client-side load balancing — an L4 load balancer pins a single connection. Config comes from env (12-factor); secrets from External Secrets / Sealed Secrets.

**Every service Deployment carries `reloader.stakater.com/auto: "true"`, and generated ConfigMaps and Secrets carry fixed names** (`disableNameSuffixHash: true`). Configuration is read from env once, when the container starts, so a Secret that changes underneath a running pod changes nothing until something restarts it — and rotating a signing key would otherwise report success while every replica kept signing with the old one.

Kustomize's content-hash suffix would also roll the Deployment, by renaming the object on each change, but it gets there by creating a new Secret every time and orphaning the last one. Nothing deletes those: each rotation leaves the superseded signing key in the cluster permanently, readable by anything that can read Secrets. `kubectl apply --prune` is not the way out — kubectl's own help calls it alpha and advises against it, and a label selector would miss a generated Secret anyway, since generator output carries only the labels of the kustomization that declares it.

So the name is fixed and the restart comes from the operator. The cost is that a rollout now depends on something outside kubectl: `make deploy` refuses to run when Reloader is not Available, because the failure it would otherwise produce is a deploy that reports success and changes nothing.

Two couplings between settings that are easy to break by tuning one side alone:

- Graceful shutdown stops consumers first, then hands the gRPC server its cancelled context; `pkg/grpcx/server` drains within `SHUTDOWN_TIMEOUT` (25s) and forces a stop after. That has to stay under `terminationGracePeriodSeconds: 30`, or the kubelet's SIGKILL lands mid-drain and the graceful path never runs.
- Client-side balancing only spreads across replicas that existed when the caller last resolved. `MAX_CONNECTION_AGE` on the server is what forces callers to re-resolve, so scaling out actually receives traffic. A client's `KEEPALIVE_TIME` must also stay above the server's `MIN_CLIENT_PING_INTERVAL`, or the server answers a well-behaved caller's pings with GOAWAY.

Every component that needs configuration declares its own struct with `env` tags and loads it through `pkg/config` — `config.MustLoad[T](config.WithPrefix("..."))` in `main.go` or `bootstrap`, then passed down explicitly. Nothing calls `os.Getenv` at runtime, and no code branches on an environment name: differences between deploys live in the values, not in `if env == "production"`.

**A field whose value would be damaging in a log line is `config.Secret`, never `string`.** It parses identically and redacts itself through `String`, `GoString`, and `MarshalText`, which between them cover `%v`, `%+v`, `%#v`, `fmt.Sprint`, `errors.Errorf`, `encoding/json`, and `zap.Any` — every way a config struct actually reaches stdout. Reading it back is `.Reveal()`, so a grep for that name lists every place a secret is used. The type exists because the leak is never a reviewed line: it is one `zap.Any("cfg", cfg)` added while chasing a startup failure, no error is raised, and the only record is a log backend holding a signing key for a year. A public key stays a plain `string` — seeing it in a log is how a key mismatch gets diagnosed.

## Working conventions

- Adding or changing an API: edit the proto, regenerate, then update handler ↔ domain mapping. Never work backwards from generated code.
- Adding a service: copy the layering, wiring, and test structure of an existing service rather than inventing a new arrangement. Consistency across services matters more than local elegance.
- Anything cross-cutting goes in `pkg/` and must stay free of business logic; anything business-specific stays inside its service.
- Commits follow Conventional Commits.

## Comments

**A comment says what the code is for, not how it works.** The how is already on the screen, in a form that cannot go stale; a comment that narrates it is longer than the code it sits above, drifts the first time the code changes, and buries the one thing the reader came for. Write the comment a reader needs in order to *use* what follows — what it is, what it guarantees, and where they must be careful.

In that order, a comment is worth having when it carries:

1. **What this is for** — the job it does in the service, in one sentence. Every exported name gets this much.
2. **What it promises or demands** — the invariant a caller may rely on, the precondition it must meet, the thing that is deliberately *not* done here. `Roles returns a copy` is this; so is `It validates nothing`.
3. **Why it is this way**, and only where the choice would otherwise look like an accident. A decision a later reader would "fix" needs its reason recorded next to it; a decision nobody would question needs nothing.

What does not belong:

- **A restatement of the mechanism.** `// loop over the roles and copy each one` explains nothing the loop did not.
- **Rules that live in this file.** The depguard allow-list, the interceptor order, the outbox contract, the error-kind table — a package comment repeating them creates a second copy that drifts, and the package is not where anyone looks for them. Reference the rule if it is genuinely surprising in context; do not re-derive it.
- **Structure the layout already states.** `internal/domain` is a domain package because of where it sits.

A doc comment is a sentence or two. It grows to a paragraph only when there is a real decision to defend, and a decision worth a paragraph is usually worth being the *only* paragraph. Inside a function, comment the line whose reason is invisible — a length check that looks redundant, a `ToLower` that exists because of how PostgreSQL renders a column — not the lines that read fine on their own.

The package comment in `services/identity/internal/domain/user.go` is the pattern. What it should say:

```go
// Package domain holds the identity service's aggregates and the rules that
// protect them: who a user is, what makes an email or a password hash valid,
// and which of those rules the database is also holding.
package domain
```

What it should not go on to say — true, but a copy of rule 5 above, and it tells a reader nothing about users:

```go
// It imports libraries and never layers, which is enforced by depguard rather
// than left to reviewers: the standard library and google/uuid are the whole
// allow-list. That is why errors declare their kind through an ErrorKind()
// method instead of importing pkg/errorx, and why nothing in this package knows
// that users are stored in PostgreSQL or described to the world in protobuf.
```

Go's own conventions still hold on top of this: a doc comment begins with the name it documents, every exported identifier has one, and `//` with a space — never a decorative banner.
