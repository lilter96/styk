# STYK

STYK is a B2B integration platform intended to connect marketplaces and ERP systems, initially MoySklad and Ozon FBS. This initial foundation implements a PostgreSQL-backed mock demonstration only; there are no real integrations, business API endpoints, frontend, authentication, or inventory synchronization rules.

Go 1.27.3, net/http, pgx, slog, PostgreSQL 17 and goose are used in one modular monolith. The API provides liveness and database readiness; a separate single-concurrency worker polls a transactional outbox. An observation and its task commit atomically; a lease and claim token permit recovery and fence stale completions. The mock handler is deterministic and performs no external writes. The lease does not prevent overlapping executions after expiration, and none of this guarantees exactly-once external calls.

## Local setup

Requires Go 1.27.3 (Go's toolchain auto-download is supported), Docker Compose, and access to Go module downloads. Copy `.env.example` to `.env`, then:

```sh
make up
make migrate
make build
```

Export the settings for direct commands: `set -a; . ./.env; set +a`. Start independently in separate terminals with `go run ./cmd/api` and `go run ./cmd/worker`. Check `curl localhost:8080/health/live` and `/health/ready`. Submit the deterministic example with `go run ./cmd/demo`. Inspect it using:

```sh
docker compose exec postgres psql -U styk -d styk -c "SELECT o.operation_key,o.sku,o.quantity,t.state,t.attempts,t.result,t.last_error FROM inventory_observations o JOIN outbox_tasks t ON t.observation_id=o.id WHERE o.operation_key='demo-inventory-1';"
```

Running the demo again reuses the same operation key and does not create another task. `make down` stops services without deleting their persistent volume. Migrations are applied explicitly with goose; never run them against production without review.

## Configuration

`DATABASE_URL` is required; `.env.example` contains local-only credentials. `HTTP_ADDR` defaults to `:8080`. `QUERY_TIMEOUT` (5s), `SHUTDOWN_TIMEOUT` (10s), `WORKER_POLL_INTERVAL` (1s), `WORKER_LEASE_DURATION` (30s), and `WORKER_RETRY_DELAY` (2s) are positive Go durations. `WORKER_MAX_ATTEMPTS` defaults to 3 (range 1–100). Lease duration must exceed query timeout. The worker stops on cancellation and unfinished work is recovered after lease expiry; the mock handler does not make network requests. Settings in `.env` are loaded by Make, not automatically by the binaries. Do not commit credentials.

## Verification

`make test`, `make lint`, and `make build` do not require a database. For integration tests, use a dedicated disposable PostgreSQL database, apply migrations, and set `TEST_DATABASE_URL` to that database. For the local Compose instance, create and migrate a separate test database (once), then run the tests:

```sh
docker compose exec postgres createdb -U styk styk_test
make migrate 'DATABASE_URL=postgres://styk:styk_local@localhost:55432/styk_test?sslmode=disable'
export TEST_DATABASE_URL='postgres://styk:styk_local@localhost:55432/styk_test?sslmode=disable'
make test-integration
```

Tests create and remove their own uniquely keyed records; keep other workers stopped on the test database because claims are global. Make reads `.env` when present; command-line Make variables override its values. CI uses a dedicated service database, runs migrations, integration tests, and race checks. `make down` does not remove data.

## Constraints and next milestones

The mock retry policy treats a process interruption as safe to repeat; it must never be reused for arbitrary external writes. Future designs must distinguish external observations, calculated desired state, intended operations, API acceptance, unknown outcomes, and reconciliation verification. They must address duplicates, out-of-order events, partial success, concurrency, external write ordering, and differing inventory semantics. A timeout after a write can leave an unknown outcome; retry requires an explicit safe policy and reconciliation. API success alone does not verify state. No generic sync status is a substitute for these distinctions.

A suitable next milestone is a read-only MoySklad connector with careful credential handling and observation ingestion, without enabling writes. Product identity, reconciliation and safe external operations follow later.

The temporary module path `example.com/styk` is intentionally unowned. Before publication, replace it in `go.mod` and imports with the repository's actual module path and run `go mod tidy`.
