# Catalog import management in the local console

The current screen and Russian user workflow are documented in [Integrations UX V1](integrations-ux-v1.md). Backend and recovery details below remain applicable; screenshots and UI labels here describe the earlier version.

The Integrations page also supports [MoySklad catalog imports](moysklad-import-management-ui-v1.md) through the same provider-aware panel, with separate scoped history and counters. The Ozon workflow remains supported.

The Integrations page can explicitly queue `OZON_CATALOG_IMPORT` for a registered Ozon account. Select a tenant and Ozon account, open **Integrations**, choose **Import catalog**, read the notice and acknowledge the request. It reads product lists (including archived products), warehouses and available FBS stock observations using the backend's bounded importer. It does not modify the marketplace or publish stock. Page load, account selection and connection testing never start an import.

## Local setup and worker

Apply the migrations through version 22, register the Ozon account and configure the same `OZON_CLIENT_ID` and `OZON_API_KEY` in the API and worker environments. Start the authenticated loopback API and Vite as described in the [web console guide](web-console-v1.md). In a separate terminal with the same `DATABASE_URL` and Ozon configuration, run `go run ./cmd/worker`. Restart both processes after changing credentials. Only a worker configured for the selected account can claim its import. Stop or upgrade old mock-only workers before submitting imports: they do not understand the import task. See the [backend guide](import-management-backend-v1.md) for limits, leases and recovery.

## Submission and recovery

The confirmation dialog uses server defaults; it does not accept credentials or unlimited budgets. A single `crypto.randomUUID()` key and the exact request body are retained in memory per intent. Duplicate clicks while submitting are disabled. If the response is lost or persistence returns an uncertain result, the UI preserves the original scope and offers a retry of the identical request. A switch of tenant, account or page does not cancel a server operation; the banner can restore the original scope. The backend returns the existing operation on an identical replay and rejects a conflicting key. No new intent is automatically submitted after failure.

The in-memory key is not stored in browser storage and cannot survive a browser reload or process loss. If the session ends or the page reloads with an uncertain submission, sign in and inspect the original tenant and account's durable import history before requesting another import. The page displays this instruction even after its in-memory state is gone. One active operation per account is enforced by the database. This is not a claim of exactly-once external execution: lease recovery can repeat read-only traversal.

## Status, progress and history

The UI lists ten scoped operations per page using `GET /api/imports`, and displays one operation via `GET /api/imports/{id}`. It reports the backend states `QUEUED`, `RUNNING`, `SUCCEEDED`, `FAILED`, `CANCELLED` and `OUTCOME_UNKNOWN`. Unknown is an unresolved persistence-phase outcome, not failure. Active and unknown operations are polled approximately every two seconds while visible; polling stops for terminal states or a query error. On a temporary error, use the displayed retry control. An expired session requires signing in again.

The phase, limits, elapsed time, request and HTTP-attempt counts, validated pages, product and warehouse counts, stock observation counts and limiting resource come from the backend. Progress is attempt-local and may reset on lease reclaim; it is neither a percentage nor a committed batch. Failed or limited traversals do not replace a previous completed import. A successful operation displays its batch reference and traversal counts and refreshes relevant console data. Identical source content can reuse an existing eligible batch. Products and Warehouses links show the **latest** eligible batch, which can differ from a selected historical operation: the existing API does not expose a historical batch detail view. Completed traversal is not a point-in-time source snapshot or proof of complete permissions.

Queued operations offer explicit cancellation via `POST /api/imports/{id}/cancel`; running and terminal operations cannot be cancelled. If the response is lost, reload the status rather than assuming cancellation. A cancellation does not reverse a completed batch.

## Security and limitations

The existing authenticated local operator session, Origin checks and loopback boundary apply. External account identifiers are visible to the trusted local operator; they do not authorize marketplace access. API keys, bearer tokens and operator credentials remain server-side. The shared local operator secret does not provide production multi-tenant or individual-user authorization. No product or warehouse mappings, stock pools or publication tasks are created by this workflow. Ozon posting imports, customer-order jobs, scheduling and automatic recurring imports are outside this interface.

The browser regression test uses the real local Go API and worker, a disposable migrated PostgreSQL database named `styk_import_ui_e2e_v1` on `127.0.0.1`, and allowlisted synthetic Ozon and MoySklad HTTP servers. It checks cancellation, a lost submission response with identical replay, persistence, batch linkage and refresh. It does not contact live marketplace accounts. After creating and migrating that disposable database, installing the web dependencies and Playwright's Chromium, run:

```sh
STYK_IMPORT_BROWSER_E2E=1 TEST_DATABASE_URL='<local disposable database URL>' \
  go test -tags=integration -count=1 -run '^TestImportManagementBrowser$' -v ./internal/console
```

The test binds Vite to loopback port 15175 and injects the synthetic server only into the test harness; it does not change production host restrictions. Supply no live marketplace credentials when running tests.
