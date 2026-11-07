# Engineering Guide — `community_service`

Persistent technical memory for this service. Runtime, libraries, container lifecycle, testing strategy, conventions, configuration, observability, and the append-only `Pinned Decisions` table.

## Runtime & Toolchain

- **Language/runtime:** Go (module `community_service`, module path `community_service` — no VCS host prefix; this module is never published).
- **Go version:** latest stable LTS toolchain (per the service Dockerfile template, `golang:1.26-alpine` build stage, distroless static runtime).
- **Package manager:** Go modules (`go.mod` / `go.sum`).
- **Build/test entrypoints (Makefile targets):**
  - `make install` — `go mod download` (always first on a cold module cache)
  - `make test` — `go test ./... -race -count=1 -parallel 4`
  - `make build` — static binary build (`CGO_ENABLED=0 GOOS=linux go build ./cmd/server`); container image build via `docker build` is executed by CI, not in the agent runtime
  - `make run` — `go run ./cmd/server`
  - `make lint` — `golangci-lint run ./...`
- **Entrypoint:** `cmd/server/main.go`. Layout: `internal/config`, `internal/handler`, `internal/service`, `internal/repository`, `internal/models`, `internal/events` (envelope, producer, consumer), `tests/unit`, `tests/integration`, `tests/mocks`.

## Libraries & External Provider SDKs

| Concern | Package / Library | Source | Usage Notes |
|---|---|---|---|
| HTTP framework | `github.com/gin-gonic/gin` | language-skill | Use gin context for binding/validation and response writing; thin handlers only. |
| PostgreSQL driver | `github.com/jackc/pgx/v5` | language-skill | Use `pgxpool`; parameterized queries only — never `fmt.Sprintf` in SQL. |
| Migrations | `github.com/golang-migrate/migrate/v4` | language-skill | SQL migration files embed the schema in `docs/architecture.md` §5. |
| Kafka client | `github.com/IBM/sarama` | language-skill | Producer for `community_events` (idempotent producer, key = `community_id`); consumer group for `moderation_events`; SASL/TLS configurable via env. |
| Logging | `go.uber.org/zap` | language-skill | Structured JSON production config; logger injected, not global. |
| Metrics | `github.com/prometheus/client_golang/prometheus` | language-skill | Register business + RED + consumer-lag metrics at startup. |
| JWT verification | `github.com/golang-jwt/jwt/v5` | tech-lead-research | Local verification of identity-issued access tokens (asymmetric signature) against the configured current + next public keys; no call to `identity_service`. |
| Validation | `github.com/go-playground/validator/v10` | tech-lead-research | Request-body validation at the boundary (shipped with gin's binding, used directly in handlers/services). |
| Env config (local dev) | `github.com/joho/godotenv` | language-skill | Local development only; production reads env vars directly. |
| Testing assertions | `github.com/stretchr/testify` | language-skill | `assert`/`require` + table-driven tests with `t.Parallel()`. |
| Mocking | `github.com/stretchr/testify/mock` | language-skill | Mocks for repository, producer, and clock interfaces. |

There are **no external third-party provider SDKs**: `community_service` integrates no external providers (no email, no LiveKit, no S3). All inter-service interaction is PostgreSQL + Kafka + the two internal REST contracts.

New dependencies MUST be added via `go get <module>@latest` + `go mod tidy`, and verified with `technical_research_agent(command="fetch_library_docs", ...)` before pinning; record any hard pin in `Pinned Decisions`.

## Container Lifecycle & Probes

- **Statelessness:** no local disk or sticky in-memory state; every instance serves any request; safe with `replicas > 1`. Kafka consumption uses one shared consumer group (partitions distributed, not duplicated).
- **`GET /healthz` (liveness):** `200` while the process is responsive; MUST NOT fail on transient PostgreSQL/Kafka hiccups.
- **`GET /readyz` (readiness):** `200` only when the PostgreSQL pool answers and the Kafka producer/consumer are healthy; `503` once shutdown begins.
- **Shutdown:** trap `SIGTERM` and `SIGINT` via `signal.NotifyContext`; on signal — (1) readiness fails immediately, (2) stop accepting HTTP requests and pause/commit the Kafka consumer loop, (3) drain in-flight requests and the in-flight consumer batch within a bounded timeout (default 20 s, configurable), (4) close the pgx pool and sarama clients, exit `0`. Use `http.Server.Shutdown(ctx)` for the HTTP drain.
- **Container hardening:** multi-stage build (static binary), distroless/slim runtime image, non-root `USER nonroot:nonroot`.

## Testing Strategy

- **Unit tests** (`tests/unit`): cover every business branch of the domain services — permission computation (owner union, role union), hierarchy comparison (position ranking, owner supremacy, self-target guard), invite validation (expiry, exhaustion, revocation, ban), EVT-004 application mapping (kick/ban/unban/mute/unmute, dedupe, stale-version skip), envelope construction (all nine fields, `trace_id` never empty). All external dependencies are mocked via interfaces (`Repository`, `EventPublisher`, `Clock`) using testify mocks; **no real network calls**.
- **Integration tests** (`tests/integration`): handler-level tests with a mocked repository layer and a mock producer capturing published envelopes; verify routing, auth middleware (bad/missing JWT → 401; internal endpoints reject non-service tokens), pagination, and error shapes (`{"error", "detail"}`).
- **Mocks live in `tests/mocks/`** and implement the same interfaces as production dependencies; they reproduce real error behavior (constraint violations, `pgx` no-rows) so failure paths are exercised.
- **Determinism & parallelism:** inject a `Clock` interface instead of reading wall time; freeze time in tests; every test uses `t.Parallel()` and shares no mutable state. PostgreSQL, Kafka, and any external dependency are always stubbed — never real infrastructure (the agent runtime has no Docker daemon; containerized dependency setups are documented for developer/CI use only).
- **Coverage:** ≥ 80 %, focused on critical paths (authorization oracle, invite accept, sanction application).

## Coding Conventions & Project Structure

```
community_service/
├── cmd/server/main.go          # entrypoint: config, pool, router, signals
├── internal/
│   ├── config/config.go        # typed Config loaded from env, validated at startup
│   ├── handler/                # gin handlers: bind, validate, delegate, map errors to HTTP
│   ├── service/                # business logic, no I/O beyond repository/publisher interfaces
│   ├── repository/             # pgx queries, migrations consumer, transactions
│   ├── events/                 # envelope type, producer, EVT-004 consumer, DLQ handling
│   └── models/                 # domain types and request/response structs
├── tests/{unit,integration,mocks}
├── Makefile, Dockerfile, go.mod, go.sum, .gitignore, README.md
```

- Layering: handler → service → repository; handlers never touch `pgx` directly; services never parse HTTP bodies.
- Errors: services return typed sentinel/wrapped errors; the handler layer maps them to the platform error shape `{"error": "<code>", "detail": "<human text>"}` with the correct status. Never expose stack traces or internal errors to clients.
- All exported types and functions have Go doc comments; comments explain *why*, never *what*; no TODOs, no commented-out code.
- Concurrency: return errors, never panic outside init; use `errgroup`/context for bounded shutdown work.
- The event envelope is a single reusable struct in `internal/events` — never constructed ad-hoc at publish sites.

## Configuration & Secrets Management

12-factor environment variables, validated at startup (fail fast with a structured log error):

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL connection string (required). |
| `KAFKA_BROKERS` | Comma-separated Kafka broker list (required). |
| `KAFKA_CONSUMER_GROUP` | Consumer group id for `moderation_events` (default `community_service`). |
| `KAFKA_SASL_MECHANISM` / `KAFKA_SASL_USERNAME` / `KAFKA_SASL_PASSWORD` / `KAFKA_TLS_ENABLED` | Optional Kafka auth/TLS. |
| `IDENTITY_JWKS_PATH` / `IDENTITY_JWKS` | Identity public verification keys (current + next) for local JWT verification. |
| `IDENTITY_ISSUER` / `IDENTITY_AUDIENCE` | Expected `iss`/`aud` claims. |
| `INTERNAL_SERVICE_TOKEN` | Bearer token required on `/internal/*` endpoints (per-edge secret). |
| `PORT` | HTTP listen port (default `8080`). |
| `SHUTDOWN_TIMEOUT_SECONDS` | Graceful drain budget (default `20`). |
| `LOG_LEVEL` | `debug|info|warn|error` (default `info`). |

Secrets come from platform-injected env vars or mounted secret files only; `.env` is local development only and git-ignored. No hardcoded values anywhere.

## Observability & Logging Conventions

- **Structured JSON logging** (zap): fields `timestamp`, `level`, `service_name="community_service"`, `trace_id`, `request_id`, `operation`, `status`, plus error details on failures. All external interactions (PostgreSQL queries at error level, Kafka publish/consume) are logged with latency and outcome.
- **Trace propagation:** read `traceparent` / `X-Trace-Id` from inbound HTTP; include `trace_id` in every log line; mirror it into the envelope of every published event. For events emitted in response to a consumed EVT-004, copy the causing event's `trace_id` and `correlation_id` and set `causation_id` to its `event_id`.
- **Metrics:** business metrics from `docs/architecture.md` §7, RED metrics, and `kafka_consumer_lag{topic,consumer_group}`; register all at startup; low-cardinality labels only (never user/community/message ids).
- **Mandatory event envelope:** every message this service publishes or consumes is `{"envelope": {...}, "payload": {...}}`. The envelope carries exactly: `event_id`, `event_type`, `event_version`, `occurred_at`, `producer`, `trace_id`, `correlation_id`, `causation_id`, `schema_ref`. `trace_id` is **never empty** — propagated from the originating request, or minted at the edge of the flow when there is no originating request. `causation_id` is null only for the first event of a flow. A payload MUST NOT carry an envelope field name at its top level. The envelope is modeled as one reusable struct, not map literals at publish sites.

## Pinned Decisions

| Dependency / Area | Pin / Constraint | Rationale | Added By | Date |
|---|---|---|---|---|
| Go runtime | Latest stable (1.26 line) | Platform default runtime for this service | tech-lead | 2026-02-05 |
| HTTP framework | `gin` (latest stable) | Approved library from Go service standards | tech-lead | 2026-02-05 |
| PostgreSQL driver | `pgx/v5` with `pgxpool` | Approved library; parameterized queries mandated | tech-lead | 2026-02-05 |
| Kafka client | `IBM/sarama` (latest stable) | Approved library; consumer-group + idempotent producer | tech-lead | 2026-02-05 |
| JWT verification | `golang-jwt/jwt/v5` (latest stable) | Local verification of identity-issued tokens, no runtime call to identity | tech-lead | 2026-02-05 |
| Member-state authority | Single source of truth in this service; peers learn state only via EVT-002 | Eliminates divergent membership state across messaging/voice/gateway | tech-lead | 2026-02-05 |
| Role hierarchy | Position-based (lower = higher rank); owner implicitly outranks all | Matches the platform authorization model; no implicit admin role rows | tech-lead | 2026-02-05 |
