# STYK

STYK is a B2B integration platform intended to connect marketplaces and ERP systems, initially MoySklad and Ozon FBS. It includes read-only source importers, a read-only stock commitment calculator, and a PostgreSQL-backed mock demonstration. There are no external write integrations, business API endpoints, frontend, authentication, or inventory synchronization rules.

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

Tests use uniquely keyed synthetic records; stock commitment tests retain immutable audit records, so use a disposable database. Keep other workers stopped on the test database because claims are global. Make reads `.env` when present; command-line Make variables override its values. CI uses a dedicated service database, runs migrations, integration tests, and race checks. `make down` does not remove data.

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

After migrations, set `OZON_CLIENT_ID` and `OZON_API_KEY` in your environment and run `go run ./cmd/ozon-import`. Use a key restricted to the required product, stock and warehouse read permissions where available. The client ID scopes source data; the key is never persisted. Settings `OZON_HTTP_TIMEOUT`, `OZON_REQUEST_INTERVAL`, `OZON_RETRY_BACKOFF`, `OZON_MAX_RETRY_AFTER`, and `OZON_MAX_ATTEMPTS` are shown in `.env.example`. The CLI targets the official HTTPS Seller API, and its HTTP client allowlists the three catalog read methods below and the two posting read methods described later. The importer performs no marketplace write operations or matching.

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

For a future bounded read-only MoySklad contract check, create the following **manually in a test account**: two warehouses; products with distinct articles; at least one product variant; two items sharing an article; and products with nonzero stock, zero stock, and a reservation. Use synthetic names and identifiers. Populate enough records for at least two pages when checking pagination. Do not infer missing stock rows as zero or treat the traversal as a point-in-time snapshot.

The MoySklad client honors standard `Retry-After` in seconds or HTTP-date format, and `X-Lognex-Retry-After` and `X-Lognex-Reset` in milliseconds. It observes `X-RateLimit-Remaining=0` with the reset header, and uses the longest applicable delay. Invalid or absent headers fall back to bounded backoff. A delay exceeding `MOYSKLAD_MAX_RETRY_AFTER` fails rather than retrying early; limits are local to the client process.

## Ozon FBS posting observations

`go run ./cmd/ozon-postings -tenant TENANT -from 2026-01-01T00:00:00Z -to 2026-01-02T00:00:00Z` uses `OZON_CLIENT_ID`, `OZON_API_KEY`, the `OZON_HTTP_TIMEOUT`, `OZON_REQUEST_INTERVAL`, `OZON_RETRY_BACKOFF`, `OZON_MAX_RETRY_AFTER`, and `OZON_MAX_ATTEMPTS` settings, and the database configuration. An explicit window of at most one year is required. The importer uses read-only POST `/v4/posting/fbs/list` followed by `/v3/posting/fbs/get`; only these and the catalog reads are allowlisted. The v4 listing uses top-level `postings`, `cursor`, and `has_next`, not the old `result` wrapper. No live Ozon request was made to validate this implementation; the interactive Ozon API reference was unavailable from this environment, and the schemas were checked against the published snapshot and Ozon's public notices. Review the current official API before production use.

Each successful traversal and its observations commit as one batch. Every repeat import appends a new batch, even if values are identical; each observation and its line order are preserved. Duplicate posting numbers within a traversal fail the entire import. A durable identity keyed by tenant, seller account and posting number rejects assignment to a different Ozon order ID; PostgreSQL foreign keys bind each observation to that identity and its batch scope. Identical IDs in different tenant/account scopes or repeated observations of the same identity in different batches are allowed. An incomplete or cancelled traversal is not persisted, and a failed database insert rolls back the batch. No external effects are produced. Pagination completes a traversal, not an atomic or exhaustive source snapshot; missing postings are neither cancelled nor deleted.

Each detail observation records a local `observed_at` when the response is received; `observed_time_basis` identifies it as `detail_received`. Observations from the earlier prototype are backfilled with batch finish time and marked `batch_finished`, so that estimate is not represented as an exact detail observation. Batch IDs give deterministic insertion order but do not establish source chronology. The documented `in_process_at` is the source's processing-start time, not a creation or update timestamp. The reviewed detail schema does not document general creation or last-update timestamps, so both are NULL and chronological order of external changes remains unknown. Source status, substatus, order ID and number, posting and parent-posting numbers, warehouse ID, shipment and delivery timestamps, cancellation fields, offer ID, SKU and quantities are retained separately. Empty optional timestamp strings and missing cancellation objects or fields remain unknown; a smaller line composition is an observation, not proof of a partial cancellation or a stock change. Names, addresses, phones and raw response bodies are not stored.

To inspect stored observations and **current** correlations:

```sh
go run ./cmd/ozon-postings -inspect -tenant TENANT -moysklad-account MOYSKLAD_ACCOUNT_ID
```

`OZON_CLIENT_ID` selects the seller account. The read path filters tenant and seller account in PostgreSQL and restricts correlation to the selected MoySklad account. It orders by batch ID, observation ID, and line number for reproducible retrieval. Product correlation considers only a confirmed mapping and verifies the mapped product's offer ID and SKU against the latest completed Ozon catalog: `confirmed`, `unmatched`, `conflict`, or `unverified` are reported without inventing a product ID from a SKU. Warehouse correlations use current active mappings, or `unmatched`. These are **current** correlations, not historical mapping facts: changing a mapping can change a previously observed row's correlation without changing its original identifiers or lines. Suggestions are never used.

As elsewhere in STYK, administrative scope is trusted input. The database account can access underlying tables across tenants; these query filters are not row-level security or authentication and do not prevent a database user from selecting another scope. Do not expose these administrative operations to untrusted tenants until authenticated tenant-to-account ownership and database access controls exist. See [the posting coverage matrix](docs/ozon-posting-coverage.md) for verified cases and limitations.

## Read-only stock commitment calculator

The calculator records one decision for a MoySklad warehouse/item and an Ozon product/warehouse. It reads existing imports and confirmed mappings only; it makes no external requests, creates no outbox tasks, and cannot publish stock. Run it only as a trusted local database administrator. Before the first calculation, explicitly register **each** account to its tenant, after independently verifying the exact canonical account identifier and ownership:

```sh
go run ./cmd/stock-commitment register -tenant TENANT -source moysklad -account MOYSKLAD_ACCOUNT -provenance 'operator: verified ownership and import history' -include-import-history
go run ./cmd/stock-commitment register -tenant TENANT -source ozon -account OZON_ACCOUNT -provenance 'operator: verified ownership and import history' -include-import-history
go run ./cmd/stock-commitment calculate -tenant TENANT -moysklad-account MOYSKLAD_ACCOUNT -moysklad-warehouse MS_WAREHOUSE -item-kind variant -item-id MS_ITEM -ozon-account OZON_ACCOUNT -ozon-warehouse OZON_WAREHOUSE -ozon-product OZON_PRODUCT -policy-version 1 -unit piece -safety-stock 1 -max-observation-age 1h -decision-lifetime 5m
go run ./cmd/stock-commitment inspect -tenant TENANT -ozon-account OZON_ACCOUNT -decision DECISION_ID
```

`DATABASE_URL` and the usual database timeout settings are required; no marketplace credentials are needed. Provenance is an administrative attestation, not verified identity; never put credentials or customer data in it. Registration is explicit and immutable. `-include-import-history` explicitly attests that earlier account-scoped imports belong to this tenant; omit it to consider only imports started after registration. With the latter choice, a deduplicated old import remains unavailable until a new import is actually recorded. Existing posting batches already carry tenant scope. The database prevents registering the same exact source/account identifier to two tenants. It cannot independently detect aliases for the same external account, authenticate an administrator, or enforce tenant access for a user with direct database privileges. No automatic ownership backfill is performed.

Policy versions are immutable within a tenant. Reuse a version only with exactly the same unit, safety stock and durations; choose a new version when settings change. V1 accepts only whole nonnegative `piece` quantities: integer-valued decimal strings such as `10.0` are exact, but fractional, negative, malformed or unsupported unit values are blocked. No unit conversion or rounding is performed. Source observations retain their original decimal values.

Decisions are append-only and record the scoped pool, account registrations, selected import and posting-window references, raw source values, current confirmed mapping versions and provenance, policy, local observation time and age, checks and reasons. The reported age is measured from local batch completion, not a source-event timestamp. Mapping references are frozen at **decision** time; they do not prove which mapping applied when a historical posting occurred. Repeating a calculation creates another decision. Imported stock from an identical fingerprint retains the original observation time. A later missing row is unknown, not zero. Existing listing exposure remains possible even when a decision is blocked.

The production adapter currently persists only `UNKNOWN` or `BLOCKED`, with a NULL quantity. It cannot establish a consistent source baseline, source ordering, unassigned reserve coverage, reservation attribution, complete posting coverage, competing exposure, unit composition, or a defensible unseen-reduction bound. A completed traversal or a recent local timestamp does not establish those proofs. The database rejects persisted `READY_CONDITIONAL` until a later milestone introduces durable, independently verified proof sources. The pure calculator supports `READY_CONDITIONAL` only for explicitly supplied synthetic proof fixtures in tests; its proof assertions are not a verifier or a way to assert production readiness.

The decision's sorted `ReasonCodes` and per-gate `Checks` explain failures and passes. Stable missing-proof codes include `SOURCE_BASELINE_NOT_PROVEN`, `SOURCE_ORDERING_UNPROVEN`, `RESERVATION_ATTRIBUTION_UNKNOWN`, `UNASSIGNED_RESERVE_COVERAGE_MISSING`, `POSTING_COVERAGE_INSUFFICIENT`, `COMPETING_EXPOSURE_NOT_VERIFIED`, `UNSEEN_REDUCTION_BOUND_UNPROVEN`, and `SALE_UNIT_UNVERIFIED`. Missing/stale input codes include `SOURCE_OBSERVATION_MISSING`, `SOURCE_TRAVERSAL_INCOMPLETE`, `SOURCE_OBSERVATION_STALE`, `PHYSICAL_STOCK_MISSING`, and `SOURCE_RESERVE_MISSING`. Known conflicts and unsupported cases include `PRODUCT_MAPPING_CONFLICT`, `WAREHOUSE_MAPPING_CONFLICT`, `SHARED_POOL_UNSUPPORTED`, `UNIT_UNSUPPORTED`, `PHYSICAL_STOCK_UNSUPPORTED`, `SOURCE_RESERVE_UNSUPPORTED`, and `INVALID_SAFETY_STOCK`; all checks, including the remaining missing-reference and scope codes, appear in the JSON decision.

The database scopes decision evidence and mapping-history foreign keys to tenant, accounts, warehouse and item. Decisions, evidence, policies, pools and registrations reject update/delete through normal SQL. The migration's down path refuses to drop them once registrations or policies exist; any manual removal of this history requires separate review. Privileged database owners can change schema or disable triggers, and legacy observations can be edited by privileged SQL; the decision stores a frozen value snapshot and restricts deletion of referenced rows. The calculator does not yet build a commitment ledger, establish historical mapping validity, reconcile external state, or allocate a shared pool. See the [architecture and write-enablement criteria](docs/stock-commitment-architecture.md).

Migration 14 adds ordinary indexes and unique constraints to existing import and mapping tables. On a large database these can take locks and must be reviewed and planned before deployment; they were applied here only to disposable test infrastructure.
