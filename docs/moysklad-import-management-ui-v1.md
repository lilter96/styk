# MoySklad import management UI V1

The current screen and Russian user workflow are documented in [Integrations UX V1](integrations-ux-v1.md). Backend and recovery details below remain applicable; screenshots and UI labels here describe the earlier version.

The authenticated local Integrations screen supports both `MOYSKLAD_CATALOG_IMPORT` and `OZON_CATALOG_IMPORT` using one provider-aware import panel and the existing durable backend. Select a tenant and registered MoySklad account above the provider cards, then choose **Import catalog** in that account's panel. Inspect the read-only scope, acknowledge the request and confirm it. Opening the page, selecting accounts and testing a connection do not start an import.

## Local setup

Apply migrations through version 22. Configure `MOYSKLAD_ACCOUNT_ID` and `MOYSKLAD_TOKEN` in the API and worker environments and register the source account with the existing trusted local administrative workflow. Start the loopback API and Vite as in the [console guide](web-console-v1.md), then run `go run ./cmd/worker` with the same database and source configuration. Restart processes after configuration changes. Stop or upgrade older workers before enabling the new task type. The same current worker can handle Ozon when `OZON_CLIENT_ID` and `OZON_API_KEY` are also configured. Credentials and HTTP endpoints cannot be supplied through this screen.

## Submission and evidence

The panel submits `POST /api/imports` with `tenant`, `moysklad_account`, `operation_type: "MOYSKLAD_CATALOG_IMPORT"`, `acknowledge: true` and a `crypto.randomUUID()` idempotency key. It sends neither `ozon_account` nor client-selected limits. The server remains authoritative for registered scope, configured account and frozen budgets. See the [backend contract](moysklad-import-management-backend-v1.md).

MoySklad imports products, variants and their parents, warehouses, aggregate stock and warehouse stock. Customer orders are excluded. No external entities or stock are modified. Successful traversal is not an atomic source snapshot or exhaustive account visibility. The import screen shows observation counts, not publishable inventory quantities. Source stock, reserve and incoming remain distinct. A missing observation is unknown; an explicitly reported zero is zero. Warehouse availability remains NULL when not provided by the source and is not calculated by this interface.

## Progress, history and recovery

Each provider/account has a separate selected operation, bounded ten-row history and query namespace including tenant, provider, account and operation ID. `GET /api/imports` and `GET /api/imports/{id}` use the corresponding provider account field. Two provider registrations with identical account text do not share cached history. Both panels can remain open together.

States remain QUEUED, RUNNING, SUCCEEDED, FAILED, CANCELLED and OUTCOME_UNKNOWN. Active and unknown operations poll about every two seconds while visible; terminal states and query errors stop automatic polling. A temporary error has an explicit retry action. The stage list highlights the reported phase without estimating a percentage. MoySklad stages include products, variants, warehouses, aggregate stock, warehouse stock and persistence. The panel displays logical requests, HTTP attempts, validated pages, elapsed time, product/variant/warehouse counts, aggregate/warehouse observation counts and expandable execution limits. Counters belong to the current attempt and may restart after reclaim. Missing optional counters are marked not reported rather than fabricated.

A lost submission response or persistence uncertainty retains the original provider, tenant, account, operation type, exact body and idempotency key in memory. Retry sends the identical body. Account switching cannot retarget that request; the banner restores its original scope. Duplicate clicks are disabled. Session loss requires signing in and inspecting fresh scoped history before requesting another import. Request keys and credentials are never stored in browser storage. Tenant and account selection alone are saved in sessionStorage for the current tab, validated against server registrations after reload; this is a convenience, not authorization. A page reload loses an uncertain request key, so inspect durable history before a new submission.

`POST /api/imports/{id}/cancel` is offered only for queued operations and requires explicit confirmation. Running cancellation is unsupported; a queued-to-running race is reported accurately. Failed or limited imports preserve previous completed batches. Unknown outcomes are not shown as failures.

Successful operations show the operation and batch IDs, whether the batch was created or reused, observed counters and links to existing data screens. Related integration, product, warehouse, stock and diagnostic queries are invalidated. Reuse preserves the old source observation timestamp. Existing Products and Warehouses screens show the latest eligible evidence, which may differ from a selected historical operation; a historical batch-detail API is not available.

## Verification and scope of changes

The browser test uses the real Go API, actual shared worker and disposable PostgreSQL migrated through 22, with synthetic HTTP sources only. For each starting provider it verifies queued cancellation, lost-response identical replay, provider switching with identical account text, durable history after F5 and then an explicit import of the other provider by the same still-running worker. PostgreSQL checks operation/task/batch linkage, exact product and warehouse identities, variant parent, explicit zero aggregate stock, nonzero warehouse reserve/incoming, NULL warehouse availability and local observation timestamps. No mappings, stock pools, stock decisions or publication tasks are created.

Run with a dedicated local database named `styk_import_ui_e2e_v1`, dependencies installed in `web`, and Playwright Chromium available:

```sh
STYK_IMPORT_BROWSER_E2E=1 TEST_DATABASE_URL='<local disposable PostgreSQL URL>' \
  go test -tags=integration -count=1 -run '^TestImportManagementBrowser$' -v ./internal/console
```

The test-only harness injects loopback source servers without changing production host restrictions. Its Ozon and MoySklad subtests also run with Go's race detector. Screenshots use synthetic data and remain local test artifacts.

This milestone deliberately includes the previously uncommitted workspace-selection persistence fix and its refresh assertion because refresh recovery is required by this workflow. The earlier unrelated `web/src/style.css` edits remain uncommitted and preserved. New import styling lives in `web/src/imports.css`, including scoped integration controls; the committed workflow does not depend on those local stylesheet edits. No backend business logic, migrations or queue semantics changed.

 Registered account identifiers may be visible to the authenticated operator. Marketplace credentials remain server-side. No recurring imports, scheduling, customer-order jobs, automatic mapping confirmation, stock decisions or publication are provided. Lease recovery can repeat read-only traversal; exactly-once external execution is not claimed.

Verification completed: frontend lint, TypeScript typecheck, 70 component tests and production build; both browser branches with Go's race detector; Go formatting/vet, `make lint`, `make test`, `go test -race ./...`, `make build`, fresh migrations through 22, PostgreSQL integration tests and race-enabled PostgreSQL tests. The browser branches also navigate to persisted Products, Warehouses and Diagnostics and check storage contains neither provider secrets nor the submission key. No live marketplace requests were performed.
