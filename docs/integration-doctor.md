# Integration Doctor V1

`go run ./cmd/doctor -tenant TENANT -moysklad-account MS_ACCOUNT -ozon-account OZ_ACCOUNT` reads persisted evidence and prints a terminal report. Add `-format json` for machine-readable output. `DATABASE_URL` must reference a migrated PostgreSQL database. Flags `-timeout` (30s) and `-max-age` (1h) bound the run and local observation age. No API credentials are required in offline mode, and the command does not load `.env` automatically.

Both exact accounts must have trusted administrative registrations for the selected tenant. Unregistered or incorrectly paired accounts fail the registration check before data is loaded. Catalog history is included only according to each registration's `include_import_history` setting. These checks are application scoping, not authenticated end-user authorization or PostgreSQL row-level security. Use only as a trusted local operator.

## Offline synthetic demo

Create a dedicated disposable database, apply the existing migrations, then run:

```sh
go run ./cmd/doctor -demo -format json
```

The demo reserves the `doctor-synthetic`, `doctor-moy` and `doctor-ozon` identities. It refuses existing evidence in that namespace; repeat it with a fresh disposable database. It creates synthetic local observations using existing import persistence paths, explicitly confirms mappings with existing confirmation methods, records an Ozon posting and MoySklad order position, then evaluates the diagnostic. It never contacts an API or creates an outbox task. The fixture time is fixed at 2026-10-29T00:00:00Z and is also used as the demo's calculation time, making its semantic report reproducible. Generated database IDs can differ. Interrupted seeding may leave partial synthetic data; discard the disposable database before retrying.

Expected results include registered accounts and recent completed traversals, one candidate customer-order correlation, stock decision `UNKNOWN`, `SOURCE_BASELINE_NOT_PROVEN` and reservation-attribution reasons, and publication `DISABLED`. The report never contains a publishable quantity.

## Optional live read diagnostics

Only with authorization for the particular selected accounts, set `MOYSKLAD_TOKEN`, `OZON_CLIENT_ID`, and `OZON_API_KEY` securely in the local environment, then add `-live`. Never pass secrets on command lines or paste them into reports. `OZON_CLIENT_ID` must equal the selected registered Ozon account. Absent or mismatched credentials produce `NOT_CHECKED` for that source. Demo and live modes are mutually exclusive.

Live mode issues at most one request per configured source with one attempt: MoySklad GET `entity/product?limit=1&offset=0` and Ozon read-only POST `/v3/product/list` with limit 1. Existing allowlisted clients supply authentication, rate limiting, connection reuse, redirects disabled and cancellation. `-request-timeout` defaults to 5s, bounded further by the overall timeout. No retries, full imports or entity writes are performed. A MoySklad empty result cannot independently verify account identity and gives WARN. Neither probe verifies the full account permission set, every connector endpoint or stock correctness. API error bodies and credentials are never copied to the report. No live run was executed during this milestone.

## Meaning and coverage

PASS means the stated local condition was checked; WARN means incomplete evidence or a known limitation; FAIL means a detected problem; NOT_CHECKED means no verification was performed. A successful connection is only evidence of that bounded read.

The report contains latest eligible batch IDs, available batch counts, local completion ages, actual posting/order windows, aggregate entity counts, current confirmed mapping counts, products without confirmations, and correlation outcome counts. All batch `exhaustive` and `point_in_time` fields remain false. Catalog deduplication does not refresh old observation times. Empty collections remain empty; missing stock rows are never zero. Names, articles, original offer IDs, credentials and raw customer records are excluded.

Correlation evaluates at most ten postings from the latest scoped batch. Each uses the existing evaluator's maximum 100 candidate orders / 1,000 position-mapping entries. Truncation is explicit and means unexamined evidence remains. Source history checks are also bounded by the existing evaluator. Product matching suggestions are computed on demand elsewhere; the doctor counts unconfirmed products but does not generate suggestion sets.

Stock calculation evaluates only the first mapped product/warehouse pair in stable mapping-ID order. The report explicitly warns that remaining pools are unchecked. Its diagnostic policy uses whole pieces, safety stock zero and the chosen max age; it does not establish sale-unit equivalence or a publication policy. The current calculator and evidence-loading helpers are reused, within the same database transaction as the report. No decision, policy or publication operation is persisted. Existing database READY restrictions remain unchanged. Current correlations use current confirmed mapping versions, not historical identity assertions.

Older active obligations may exist outside bounded windows. Completed traversal cannot prove an exhaustive or atomic snapshot, source-event ordering, full permissions, order identity, reservation attribution, baseline inclusion, coverage of unassigned reserves or competing exposure. Existing listings may remain exposed despite UNKNOWN or BLOCKED. No successful doctor result authorizes synchronization.

For meaningful test-account validation, manually populate two warehouses, products/variants with unique and duplicate articles, nonzero/zero stock, an order reserve and partial shipment. Populate Ozon test observations with explicit authorization using the existing bounded read importers. Inspect mappings explicitly. Then run offline diagnostics and, separately authorized, the bounded live probes. Those tests can establish account access and response compatibility; proving reserve reflection still requires independently verifiable cross-system and stock-baseline evidence.

## Automated evidence

`TestDoctorSyntheticEndToEndAndIsolation` exercises persisted imports, confirmations, order correlation and UNKNOWN calculation. `TestDoctorEmptyRegisteredAccounts` checks missing imports and an incorrect account selection. `TestDoctorRegisteredTenantIsolation` checks a pair assembled from two tenants. `TestDoctorTruncationAndNoScheduledEffects` checks the posting limit and absence of persisted decisions/outbox tasks. Report unit tests cover stale, partial and missing evidence, ambiguity, stable JSON and redaction; HTTP probe tests use sanitized local servers only. Run the existing `make lint`, `make test`, `go test -race ./...`, `make build`, and PostgreSQL integration commands. No extra migration is required.
