# Orders: shipment table and detail workspace

Based on `master` at `cab972a`. The two supplied STYK references guide the layout; their example orders, amounts, product images and counters are not production data.

## MoySklad-primary lifecycle configuration

`/orders/settings?section=lifecycle` adds an independent owner opt-in for exact
native-state transitions. The existing settings pattern exposes scheme, action,
target/predecessor IDs and explicit whole-posting evidence. It validates forward
state conflicts, cycles and incompatible event semantics. Separate toggles cover
physical handoff demand and verified physical return; Ozon acceptance is visibly
unavailable in this version. Revocation does not erase execution history.

The transition journal and authoritative monthly realization preview are separate
read-only sections outside the editable form. `QUEUED` is a work binding, not
completion. Finance amounts remain decimal strings and the preview exposes its
accounting blockers without offering an unimplemented native report write.

The lifecycle section uses a static save footer so expanded mappings and the last
read-only rows remain visible on mobile. Existing settings sections retain their
existing footer behavior. Browser evidence covers 1440, 1366 and 390 CSS pixels,
exact focus/control geometry, long native state names, opt-in/save/revoke and
manual report pagination; this evidence does not establish provider acceptance.

## User workflow

- The shipment table provides server search by posting number, commercial order ID and the observed order number; status and attention filters; page selection; cursor navigation; and the existing bulk mirror preview.
- Opening a posting shows a 640px right drawer with Overview, Products, Actions and History. The table retains its filters, cursor and selection. Related/parent/child links open the related posting, fetching a scoped search page if necessary.
- Action forms mount only after visiting Actions and remain mounted across drawer tab changes. They reset on source revision or successful approval as before. Closing the drawer discards that drawer's draft and invalidates its preview.
- The header's Settings link opens the separate `/orders/settings` workspace described below; the shipment table contains no embedded configuration form.
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

## Order settings workspace (2026-11-06)

The five approved settings references now guide `/orders/settings`. This replaces
embedded settings below the shipment table. The six sections use the `section`
search parameter, preserve one draft, and share a sticky save/cancel bar. Scope
changes and navigation guard unsaved work. Loading, missing mappings, unavailable
metadata, saved state and revision conflicts use the actual scoped API results.
Ozon observation and document transmission have independent switches, including
when a legacy configuration has no separate read-permission record.

The primary fields and status rules have dedicated compact layouts. Existing
address/PII retention, extra source fields, service-attribute setup and its
unknown-outcome reconciliation, native documents, reservation/posting choices and
technical intervals remain available. Reverse status rules remain suggestions;
saving settings never approves Ozon operations or warehouse events.

### Warehouse routing

`orders.ReviewContext.PostingWarehouse` resolves exactly one confirmed canonical
mapping for the posting and account pair. Normal mirrors, split reserve paths,
native movement planning and dispatch validation use this evidence. Native target
reference comparison still rejects silently moving an existing order to another
store. Missing, foreign, ambiguous or changed mapping evidence fails closed for
that posting. No fallback store is used.

`Config.StoreID` remains in JSON for legacy round trips and audit history, but is
neither required nor consulted for routing. There is no destructive backfill or
second mapping subsystem. `GET /api/orders/warehouses` provides a scoped, keyset
paginated local read of warehouses, mapping confirmation and actual counts;
opening settings does not call providers per warehouse. Editing links remains in
`/warehouses`. Review-confirmed canonical links remain immutable; adversarial
unreviewed-link drift is checked before dispatch.

### Permanent mirror authorization and migration 69

A permanent grant explicitly stores `perpetual: true` and a SQL NULL expiry.
Legacy grants retain their original expiry and are never upgraded by reading or
resaving unrelated settings. Opt-in and expansions require `automation_consent`
from the authenticated owner. Account scope, owner membership, current config and
grant revisions, action/field allowlists, credential locks, mapping evidence and
the final dispatch fence still apply. Removing the grant in an atomic config save
revokes queued work by changing the current head; immutable grant history is not
rewritten. An already dispatched unknown outcome remains a readback operation.

Permanent policies only admit mirror create/update. Update includes an exact,
ownership-proven patch of existing positions with the selected field allowlist;
it does not authorize structural replacement, Ozon writes or native documents.
Legacy grants retain their exact-action meaning. Native document confirmations
keep their existing lifetime and explicit event approval model.

Migration `00069_orders_perpetual_automation.sql` is additive and preserves all
legacy records. Apply it before starting this binary; readiness requires schema
69 and the lifetime constraint. Its rollback succeeds before permanent history
exists. Once used, rollback explicitly refuses to rewrite immutable audit data,
even after automation is disabled; retain the schema and use a reviewed forward
fix. This is an intentional deployment limitation.

### Verification entry points

Use `make check-plan`, `make check`, `make check-ui` and the full high-risk lane.
The scoped integration runner deliberately refuses a partial result for this
schema/security impact; `make check-full` supplies the required integration and
process/browser coverage. The test runner also removes alternate
`*_TEST_DATABASE_URL` overrides, and selection fixtures honor the supplied
disposable database before considering a legacy local test-DSN file. The Docker
runner prepares the companion native-point database; when supplying a dedicated
DSN manually, prepare `styk_orders_point_test` on that same test server.

The settings scenario in `web/e2e/orders-console.spec.ts` captures all five
sections at 1440, 1024, 768 and 390 pixels, exercises save/reload, draft retention,
consent, disable, deep links, keyboard interaction and navigation. Captures are
under `web/test-results/**/settings-{section}-{width}.png`; the reviewed copies
are preserved in `.local/orders-settings/screenshots/`.
Additional contracts cover multi-warehouse provider dispatch/readback, unmapped
posting isolation, mapping drift, scoped pagination, legacy opt-in, permanent
policy persistence, claimed-work revocation and immutable-history rollback.


### Recorded verification and local rollout

The scoped fast lane passed Go lint, race-enabled unit tests/build and frontend
checks. The final frontend lane passed all 371 tests, typecheck, lint and build.
The browser lane passed 34 scenarios; the settings scenario passed again after
its final layout adjustment. Seven focused PostgreSQL contracts passed on the
owned disposable server. The partial integration target refused this high-risk
impact as designed. Full regression was stopped at the owner's explicit request
after Go package checks, including `internal/postgres`, passed; the remaining
integration/process lane and the full gate are **not passed**.

| Regression | Recorded evidence |
| --- | --- |
| Multiple warehouses | PASS: normal provider dispatch/readback created two orders on distinct mapped stores; one unmapped posting stayed blocked |
| Legacy expiry to permanent opt-in | PASS: domain and PostgreSQL consent/persistence contracts |
| Revoke queued/claimed work | PASS: current head prevents new dispatch and no write intent is created |
| Mapping drift | PASS: queued work is blocked when canonical confirmation no longer matches |
| Scope isolation | PASS: foreign scope rejection and scoped pagination contracts |
| Split and native documents | Go unit/PostgreSQL package checks passed; full integration/process regression stopped and remains unverified as a whole |

At the owner's explicit request the local application database was backed up,
then upgraded from schema 68 to 69. The new API was warmed up, promoted to
`127.0.0.1:8080` and passed readiness. Its console origin is the current UI at
`http://127.0.0.1:5175`. Stored order settings and source permissions were verified
unchanged. Browser verification read the actual warehouse list successfully;
no settings were submitted. Deployment metadata, the verified backup and the
migration log are in `.local/orders-settings/live/`. No commit, push or merge was
performed.
