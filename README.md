# STYK

STYK is a B2B integration platform intended to connect marketplaces and ERP systems, initially MoySklad and Ozon FBS. The foundation includes a read-only MoySklad importer and a PostgreSQL-backed mock demonstration. There are no external write integrations, business API endpoints, frontend, authentication, or inventory synchronization rules.

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

Reconciliation and safe external operations are future milestones.

The temporary module path `example.com/styk` is intentionally unowned. Before publication, replace it in `go.mod` and imports with the repository's actual module path and run `go mod tidy`.

## Read-only MoySklad import

Apply migrations, set `MOYSKLAD_TOKEN` in your environment (never commit it), and set `MOYSKLAD_ACCOUNT_ID` to the MoySklad account UUID associated with that token. Then run `go run ./cmd/moysklad-import`. The command makes GET requests only and does not create outbox tasks or contact marketplaces. Only the official HTTPS endpoint is used by the CLI; tests use an HTTP mock. No token is stored in PostgreSQL. Account IDs are checked against any returned entity `accountId`; an empty or restricted result cannot independently validate the operator-supplied account ID.

The connector reads `entity/product`, `entity/variant`, `entity/store`, and the aggregate `report/stock/all` with `stockMode=all;quantityMode=all` and the report's default variant grouping. It persists one atomic import with source identifiers and timestamps. Inspect `source_imports`, `source_entities`, and `source_stock_observations`; a repeated identical latest import is deduplicated, so its original timestamps do not advance. If values later return to an earlier state, a new import is recorded. Errors or incomplete pages leave no partial import. The aggregate report is across warehouses; its values are not per-warehouse stock. Product names and codes are source data, not product matches.

MoySklad defines `stock` as physical stock, `reserve` as reserved, `inTransit` as expected incoming, and `quantity` as available considering reservations and incoming stock. The short report also defines `freeStock` as stock less reserve; this importer uses the extended report and does not calculate or request `freeStock`. The columns preserve each returned value independently, including negative and fractional numbers. See the [official stock report specification](https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/reports/_report_stock.md).

Completed pagination does **not** mean an authoritative point-in-time snapshot: source data can change during traversal, visibility depends on permissions, and MoySklad says items being recalculated may be absent. Missing rows must never be interpreted as zero. The code detects changing page counts, duplicate IDs, absent required fields, and interruptions, but cannot detect all concurrent changes or guarantee completeness. Imports are bounded to 200,000 rows per collection and 16 MiB per response, retained in memory until the database transaction. The default database query timeout applies to the entire commit and may need increasing for large imports. Per-account rate limiting is local to one process and does not coordinate other instances or API users; adjust `MOYSKLAD_REQUEST_INTERVAL` (default 400ms) for shared limits. HTTP timeout defaults to 30s; backoff defaults to 1s, attempts to 3, and maximum accepted `Retry-After` to 1m. See `.env.example` for variable names. 401/403 are not retried. Long Retry-After values cause a safe failure rather than an early retry. A bounded read-only test against an empty test account verified authentication, empty collection envelopes, and warehouse identity; product, variant, stock row and multi-page contracts still require live verification with suitable test data.

The import also traverses [MoySklad's `report/stock/bystore`](https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/reports/_report_stock.md) with `stockMode=all` and `groupBy=variant`. It stores each product or variant and warehouse pair in `source_warehouse_stock_observations`, using the warehouse UUID from its `meta.href` and preserving both source URLs. The import batch identifies the account; `observed_at` is the local traversal completion time, not a source timestamp. The existing `source_stock_observations` table continues to hold aggregate observations, including historical ones.

The documented warehouse report supplies `stock`, `reserve`, and `inTransit`; it does not supply warehouse-level `quantity` (available). That column is NULL, not zero or an inferred calculation. The short warehouse report is a separate, non-paginated API with different representation and possible unassigned warehouse rows; it is not ingested here. No warehouse-level `freeStock` is calculated. An absent item or warehouse row is unknown, including when `stockMode=all` is used. The batch records successful traversal, not a consistent or exhaustive snapshot. Per-warehouse rows are committed atomically with the aggregate report and entities, and identical consecutive complete imports are deduplicated.

## Read-only Ozon import

After migrations, set `OZON_CLIENT_ID` and `OZON_API_KEY` in your environment and run `go run ./cmd/ozon-import`. Use a key restricted to the required product, stock and warehouse read permissions where available. The client ID scopes source data; the key is never persisted. Settings `OZON_HTTP_TIMEOUT`, `OZON_REQUEST_INTERVAL`, `OZON_RETRY_BACKOFF`, `OZON_MAX_RETRY_AFTER`, and `OZON_MAX_ATTEMPTS` are shown in `.env.example`. The CLI targets the official HTTPS Seller API, and its HTTP client allowlists only the three read methods below. The importer performs no marketplace write operations or matching.

* `POST /v3/product/list` reads `ALL` (which excludes archived) and `ARCHIVED` separately, using `last_id` and `total_items`. Ozon announced retirement of `total` on 23 November 2026. Duplicate product IDs or seller offer IDs fail the import.
* `POST /v2/warehouse/list` reads FBS and rFBS warehouse identities, statuses and the reported rFBS flag, with `cursor` and `has_next`.
* `POST /v2/product/info/stocks-by-warehouse/fbs` uses batches of up to 100 offer IDs and follows `cursor`/`has_next`. It records Ozon's `present` (warehouse total), `reserved` (reserved), and `free_stock` (available for sale) as distinct reported values. Product ID, offer ID, SKU, and warehouse ID remain distinct; missing SKUs remain NULL. No stock is inferred for missing rows.

The previous v1 warehouse and FBS warehouse-stock methods are retired; see [Ozon's replacement notice](https://dev.ozon.ru/news/648-Izmeneniia-v-metodakh-Seller-API/) and its [total field notice](https://dev.ozon.ru/news/801-Otkaz-ot-polia-total-v-metodakh-Seller-API/). The official interactive API documentation was inaccessible from this development environment; request and response fields were checked against an independently hosted [Seller API OpenAPI snapshot](https://github.com/MissiaL/ozon-api/blob/main/references/ozon-seller-openapi.json). No live Ozon verification was performed. Confirm the current official contract and credential permissions before production use.

Successful imports are atomic and stored in `source_imports`, `ozon_import_batches`, `ozon_product_observations`, `ozon_warehouse_observations`, and `ozon_fbs_stock_observations`. The batch's `completed` status means API traversal completed, not that the data is complete, synchronized across endpoints, or a point-in-time snapshot. Product counts and duplicate IDs detect some changes, not all changes during pagination. Missing stock rows and omitted products remain unknown. Repeating the same latest content creates no new batch and does not advance its observation timestamp. Stock observation times are local import completion times. Collections are bounded to 200,000 rows and each HTTP response to 16 MiB; imports are held in memory and written within the configured database timeout. Rate limiting is per process, not shared among instances. Non-2xx errors do not persist a batch, and 401/403 are not retried.

## Product matching V1

After importing both sources and applying migrations, inspect candidates with the local administrative CLI:

```sh
go run ./cmd/matching inspect -tenant TENANT_ID -ozon-account OZON_CLIENT_ID -moysklad-account MOYSKLAD_ACCOUNT_ID
```

To confirm one explicitly reviewed candidate, pass the Ozon product ID, MoySklad kind and ID, and a meaningful operator/reason string:

```sh
go run ./cmd/matching confirm -tenant TENANT_ID -ozon-account OZON_CLIENT_ID -moysklad-account MOYSKLAD_ACCOUNT_ID -product OZON_PRODUCT_ID -target-kind variant -target-id MOYSKLAD_VARIANT_ID -provenance 'operator: reason'
```

Only trusted administrators with database access should use this CLI. Tenant and account identifiers are explicit operator-supplied scopes; STYK does not yet authenticate operators or maintain a tenant-to-account ownership registry, so an operator must verify the scope. Provenance is an operator-supplied audit statement, not a verified identity. There are no public approval endpoints.

The engine compares Ozon `offer_id` against MoySklad product or variant `article` and `code`, trimming only leading and trailing whitespace. It retains original values and evidence, preserves case and punctuation, and returns every candidate in stable order. Ozon SKU and product ID are never used as seller articles. Neither importer stores barcodes, so barcode matching is unavailable; no barcode is invented. A unique candidate is a suggestion, not a confirmation. Result statuses distinguish missing input, no candidate, unique candidate, ambiguity, confirmed mapping and conflicts. Shared targets are flagged for review, including on unconfirmed candidates; they are not a stock synchronization policy.

Confirmation re-reads the latest completed imports and revalidates the selected candidate under database locks. It creates a versioned current mapping and an audit history entry containing both import IDs and provenance in one transaction. Different Ozon offers may map to one MoySklad item; each Ozon product and offer may have at most one mapping per tenant and Ozon account. Existing mappings cannot be overwritten by this CLI, including when evidence changes. Mapping replacement and revocation are not implemented; version 1 and its confirmation are retained. Absence from a later import never deletes a mapping. Imports may be incomplete or change during traversal, so suggestions are not proof of identity. Suggestions are generated on demand and not persisted. No stock publication is enabled.

## Warehouse mapping V1

A trusted administrator can explicitly confirm a one-to-one mapping using `go run ./cmd/warehouse-mapping -tenant TENANT -moysklad-account ACCOUNT -ozon-account ACCOUNT -moysklad-warehouse ID -ozon-warehouse ID -provenance 'operator: reason'`. Inspect identities and names in `source_entities` and `ozon_warehouse_observations` from the latest completed imports first; names never activate mappings. Ozon warehouses must have `is_rfbs=false`. Confirmation and audit history commit atomically, and existing mappings cannot be replaced or revoked in V1. Repeated confirmation is idempotent. Scope is the tenant and account pair; operators are trusted to supply authorized scopes because tenant ownership and authentication are not yet implemented. Import absence does not delete a mapping. No allocation or synchronization is performed.
