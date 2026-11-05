# Orders: shipment table and detail workspace

Based on `master` at `cab972a`. The two supplied STYK references guide the layout; their example orders, amounts, product images and counters are not production data.

## User workflow

- The shipment table provides server search by posting number, commercial order ID and the observed order number; status and attention filters; page selection; cursor navigation; and the existing bulk mirror preview.
- Opening a posting shows a 640px right drawer with Overview, Products, Actions and History. The table retains its filters, cursor and selection. Related/parent/child links open the related posting, fetching a scoped search page if necessary.
- Action forms mount only after visiting Actions and remain mounted across drawer tab changes. They reset on source revision or successful approval as before. Closing the drawer discards that drawer's draft and invalidates its preview.
- The header's Settings button exposes the existing complete `OrdersConfig`. Closing settings or switching the page tab preserves unsaved settings. Dirty settings continue to block previews.
- Checking the source schedules the existing Ozon read. Refreshing the list reads stored observations. These controls have separate loading states and feedback.
- Global history stays subscribed while hidden so its latest outcomes and account-scoped links continue to protect operations. Drawer history uses the existing exact-posting `search` parameter and its own cursor; other targets in a shared operation are excluded from that view.

## Existing operation boundaries

`OrdersActions`, `OrdersNativeDocuments`, `OrdersComposition`, `OrdersPreview`, eligibility, recovery, label expiry, native retry, country/exemplar controls and split evidence are reused. Selecting rows or opening a drawer never submits an operation. Explicit preview and subsequent approval retain configuration revision, expiry, permitted actions, idempotency and unknown-outcome handling. A generation check discards an in-flight preview after its form/configuration context was invalidated.

The account-keyed `OrdersAccount` still unmounts all selection, drawers and previews on tenant or connection changes. API responses validate scopes and target-state completeness. UNKNOWN fences remain nonselectable, including in select-all. ACKNOWLEDGED continues to mean accepted and awaiting reconciliation, rather than completed. Table outcomes describe the latest recorded operation; the detail view also exposes the native order link and source/observation timestamps. Missing financial fields are not converted to zero. Decimal money, quantities and identifiers never pass through floating-point conversion for display.

## Read API extension

`GET /api/orders/postings` adds optional `status` and `attention=true`. Filters run in SQL before `LIMIT` and keyset pagination. The cursor binds tenant, both accounts, search and both filters; unfiltered cursor bindings retain their previous representation. No migrations, new dependencies or external writes were added.

Attention includes unresolved UNKNOWN fences, latest UNKNOWN/BLOCKED/REJECTED outcomes, source changes since the latest operation, quarantined observations and source schema drift. It does not imply a fresh provider read. Source freshness is additionally shown in the detail overview and existing eligibility checks. No global total or global attention count is invented; pagination shows only the current page and observed page size.

## Verification

Run the frontend checks with `npm run typecheck`, `npm run lint`, `npm run test` and `npm run build` in `web`. `web/e2e/orders-console.spec.ts` exercises the real React UI with isolated synthetic HTTP fixtures, including existing Ozon actions, native documents, composition replacement, labels, country, exemplars, recovery and separate approval. Its visual acceptance scenario captures table, selection, drawer, actions, preview and history, checks keyboard focus restoration, console/HTTP errors and responsive layouts at 1600×1000, 1440×900, 1280×800, 768×1024 and 390×844. Screenshots are written to `.local/orders-redesign`.

The Go cursor/filter tests include SQL filtering beyond the first 25 postings, combined filters, account isolation, unknown statuses, real work-state attention conditions and search by the displayed order number. They use the existing dedicated-test-database/schema fixtures. The process/browser suites are `TestOrdersNineProcessStack` and `TestOrdersMerchantCurrentStateBrowserStack`, with their explicit process/browser flags and a dedicated localhost orders test database. They exercise actual APIs, PostgreSQL and workers against loopback providers. Root `make lint`, `make test`, `make build`, `go test -race ./...` and `go build ./...` remain applicable.

No live marketplace or MoySklad mutation is part of verification. Production volume/query latency and current provider behavior require normal release monitoring; this change does not claim live-provider acceptance.

### Recorded local results, 2026-11-05

| Check | Result |
| --- | --- |
| Frontend TypeScript and ESLint | PASS |
| Frontend unit/integration | PASS — 335 tests in 38 files |
| Frontend production build | PASS |
| Orders UI Playwright | PASS — 16 scenarios, all five viewport sizes |
| Dedicated PostgreSQL filter/cursor tests (`-race`, integration tag) | PASS |
| Nine-process stack (`-race`) | PASS — all phases, including browser orders and merchant smoke |
| Merchant current-state process/browser stack (`-race`) | PASS — unknown readback, retained history page and native retry after known rejection |
| Root lint (`go vet` and golangci-lint) | PASS — 0 issues |
| Root `make test`, `go test -race ./...`, `make build`, `go build ./...` | PASS |
| Visual inspection | PASS — real browser captures; no document horizontal overflow or unexpected console/HTTP errors in the visual acceptance scenario |

The Playwright total is 19 scenarios: 16 isolated UI cases and three process-backed browser cases. Screenshots of process-backed unknown-history and retry states are also copied into `.local/orders-redesign`. No live-provider credentials or production database were used, and no remote branch was pushed.

### Hover contrast correction

The shared `imports.css` fallback no longer replaces every hovered button's background with dark purple. Component variants retain their own surfaces and foregrounds; the shared fallback only adjusts brightness. This fixes selected/unselected tabs, transparent row actions, attention buttons and other custom buttons throughout the shell. Filled primary controls keep their existing dedicated hover treatment.

The Playwright hover audit uses real pointer movement, waits for CSS transitions, and checks text/icon contrast against the underlying composited backgrounds (including gradient stops). It covers the seven workspace routes plus orders selection, all drawer tabs, settings, preview and history. Overlays are audited within the active dialog so the test does not attempt to hover inaccessible background controls.

Follow-up verification passed: all 29 orders/shared-shell Playwright scenarios (including both hover audits), 335 frontend tests, TypeScript, ESLint and production build. Root lint, unit/race tests and builds also passed. The hover-state screenshot is `.local/orders-redesign/hover-fixed.png`.

### Local preview with authentic account data

At the owner's request, the localhost preview on port 5183 now reads the authenticated Go API on port 8183, using the existing account database and its genuine operation history. The original API and workers were preserved. Both preview processes run as separate user services, independent of the tool session. Private launchers, session material and read-verification results are ignored under `.local/real-data`; the Vite file server denies that directory. Browser mutations are rejected by the preview gateway.

Normal read-only catalog imports completed on 2026-11-05: Ozon reported 33 products and 13 warehouses; MoySklad reported 14 products, 4 variants and 3 warehouses. The existing two October postings are cancelled, with five historical STYK operations. Fresh exact provider reads confirmed both posting statuses and product identities/quantities, both linked unposted customer orders, and the existing unposted demand document. No new provider write was dispatched; the durable provider-write count remained unchanged. These bounded reads do not establish account-wide order coverage or physical shipment.

Unavailable order totals remain unknown, and source drift continues to require attention. Compatibility diagnostics are collapsed under a visible summary so authentic payloads with many diagnostic fields do not push the product composition out of the overview. All diagnostic details remain available. TypeScript, ESLint, 335 frontend tests and the frontend build passed after this presentation adjustment. Root lint, unit/race tests and builds also passed for the local API used by this preview.

## Transfer history journal

The history redesign starts from `89fe341` (the current `origin/master` at implementation time). The approved journal and operation-detail references guide this view. The shipment table, shipment drawer, settings and mutation workflows retain their existing composition.

`OrdersOperations` renders the journal; `OrdersHistoryTable` expands each operation into one row per work, keyed by both real IDs. A batch can therefore show different outcomes for different postings. Empty operations remain inspectable with a neutral result. Pagination still counts server operations, not rendered rows or a fabricated global total. Date groups preserve server order and use local dates. The only process timestamp shown is the operation's real creation time.

`OrdersHistoryPresentation` owns seller-facing states and action names. Only `RECONCILED` is “Готово”; `ACKNOWLEDGED` waits for confirmation, `UNKNOWN` requires checking, and rejected/blocked actions retain their distinctions. Unknown future states stay neutral. Completed row titles use the past tense; pending titles describe the intended action. Country reconciliation retains the narrower “requirement resolved” meaning.

`OrderHistoryDetailDrawer` uses a native modal dialog with background interaction blocked, explicit Tab wrapping, Escape, scroll containment and focus restoration. The result explanation, known problems, permitted documents and existing `OrdersRecovery` remain outside technical diagnostics. Position patches, action parameters, native document metadata, split evidence, task IDs and the complete returned work remain available under “Технические сведения”; that content mounts on expansion. Artifact downloads still require reconciliation, availability, a safe same-origin URL and a valid retention period. MoySklad document links require valid UUIDs. A failed refresh displays an error alongside the last received result.

### History read contract

`GET /api/orders/operations` now applies the existing optional `status` and `attention=true` query fields. History attention means `UNKNOWN`, `REJECTED` or `BLOCKED`; it does not add the shipment list's source-drift semantics. Search remains an exact posting-number match. Combined filters must match the same work. SQL selects matching operations before `LIMIT`, then returns only matching targets. Tenant and both account predicates apply to selection and hydration. Cursors already bind these filters and cannot be reused across searches, result filters or account scopes. No schema migration is needed.

`useOrdersHistory` keeps a separate unfiltered first-page subscription, including while history is hidden, filtered or paginated. Its latest outcomes and links continue to feed posting safeguards. The displayed query has its own scope/filter/cursor key, cancellation signal and bounded polling. Switching accounts remounts the journal and clears its filters, cursors and operation panel. The original posting-specific history was moved unchanged into `OrdersPostingHistory`; it continues to use exact posting search.

Refreshing history or opening details reads saved STYK state. It does not request a new external write or imply that a provider check was just scheduled. Unknown-result recovery and native retry continue to use the existing backend eligibility, evidence, explicit approval and idempotency boundaries. No mutation endpoint, dispatch logic, dependency or account restriction changed.

### Selected verification and captures

Use `make check-plan` and the lanes in `docs/testing-policy.md`. `make check` selects Go lint, race-enabled unit tests and build for the affected packages plus the frontend checks. The frontend lane can be resumed independently with `go run ./cmd/checks run --lane web`; unchanged passed Go checks need not be repeated. `make check-integration` selects the new history contract plus the two existing posting-filter contracts and owns a disposable local PostgreSQL instance. `make check-ui` selects the orders and shared-shell presentation specs. Full regression and process-backed provider/browser acceptance are separate lanes and are not claimed by these presentation fixtures.

`OrdersHistory.test.tsx` covers all nine work states, future/missing data, per-work identity, empty batches, dates, combined filters, cursor reset, details, deferred diagnostics, recovery visibility, safe artifacts, account mismatch, resolved filtered selections and failed refreshes. The additional Playwright scenarios cover the journal, success and unknown details, technical expansion, recovery, cross-page search/filtering, unfiltered background polling and account switching. Existing label expiry/download, split evidence and audited recovery scenarios now open the operation panel explicitly.

Real React/Chromium captures are reproducible under `.local/orders-history`: `01-history-1600.png`, `02-attention.png`, `03-success.png`, `04-unknown.png`, `05-technical.png`, `06-recovery.png`, `history-mobile-full.png`, and history/drawer images at 1440×900, 1280×800, 768×1024 and 390×844. The presentation runner has no application API proxy; all scenario data and downloads are isolated fixtures. Live services and production-scale query latency are outside this verification.

Recorded results for the transfer-history change: TypeScript, ESLint, all 355 frontend tests and the production build passed through the scoped web runner. The selected Go lane passed golangci-lint (0 issues), unit tests with `-race` and build for all 18 planned packages. All three selected PostgreSQL contract tests passed with `-race` against disposable PostgreSQL. All 32 selected orders/shared-shell Playwright scenarios passed; after adjusting only capture scroll positions, the 20 orders scenarios passed again and the unchanged shared-shell results were reused. Visual review covered both references, all five requested viewport sizes, the attention filter, success, unknown, expanded diagnostics, recovery and mobile. Full regression, process-backed end-to-end acceptance and live-provider validation were not run for this scoped change. No changes were pushed, merged or deployed.
