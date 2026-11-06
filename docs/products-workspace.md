# Products workspace

Baseline: `3f94c439bb6a60f5eb4587ed32fd4bc623137ebd`, fetched from
`origin/master` on 2026-11-06. Work starts on
`codex/products-unified-workspace` with a clean working tree.

## Audit and implementation map

| Scenario | Existing path | Gap / change |
| --- | --- | --- |
| Product review | `/products` → ProductMappingPage → `/api/products` → matching.Engine → observations/product_mappings | Unified workspace, preserve review-bound confirmations and shared inventory |
| Field rules | `/catalog-sync` → CatalogSyncConfig → catalog-sync/config → catalogsync.Service → config revisions | Move into Products; preserve revision checks and per-field consent |
| History | CatalogSyncOperations → catalog-sync/operations/reconcile → runner/reconciler → work/outbox | Dedicated tab and readable states; accepted is not verified |
| Creation | CatalogSyncCreation/Facts → config/preview/approve → provider Validator/CreationFinder → frozen commands/outbox | One-time server-authoritative batch without enabling or replacing field rules |
| Matching | matching.Engine + mapping reviews → import/matching advisory locks → mapping history and confirmation audit | Pair-scoped opt-in, frozen preview and safe exact revalidation |

The source identity is an Ozon product, with separate offer and SKU. The target
is a MoySklad product or variant. Multiple Ozon products may share a target;
the source mapping cannot be overwritten. Matching normalization trims only
whitespace. Name similarity must never authorize an automatic confirmation.

Catalog creation already has adapter validation, durable dispatch intents,
provider task polling and readback. Expired dispatches become UNKNOWN, not a
second write. Existing browser bulk import changes enabled/test_enabled and
loops over individual approvals: this is the path to replace. Creation's
resolved association currently lives in catalog_sync_associations; it must not
be described as a confirmed inventory mapping before that mapping exists.

## Approved visual direction

All 19 reference images reviewed; v3 takes priority for toolbar, import hub,
matching and mobile. Use existing tokens: background `#f6f8ff`, surface white,
ink `#111936`, primary `#693cff`, lavender `#f2eeff`, line `#e9edf8`. Existing
Inter serves headings and body; do not add a font or a UI framework. Reserve
violet for the main action, active tab and selected rows. Use semantic green,
amber and red status tokens with text labels.

Desktop: left-aligned page title and account pair, compact factual summary,
then a workspace with tabs/actions, search/filters, wide rows and selection
footer. Mobile: wrap actions, turn rows into labelled cards and keep all
controls reachable without page overflow. Drawers retain focus management.

```text
Товары                 МойСклад ↔ Ozon
подтверждено / без пары / передача
Все товары | Правила | История     Автосопоставление  Импорт ▾
Поиск                    Фильтры
Товар | Во втором сервисе | Связь | Передача | Действие
Выбрано / действия                       Страницы
```

The plan follows the approved operational workspace, rather than adding
decorative dashboard cards. Counts must come from complete server aggregates;
unknown data must stay unknown. Product images are shown only when available.

## Final routes and removal

The user's explicit removal instruction supersedes the attachment's redirect
proposal. `/catalog-sync` has no route, alias or redirect. The application shows
its missing-page state at that URL. `CatalogBulkImport`, `CatalogBulkPlan`,
`CatalogBulkRequests`, their obsolete tests, and unused presentation rules were
deleted. Their useful behavior is covered by `ProductImport`, its component
tests, the redesigned workspace browser tests and the real catalog stack test.
The active `/api/catalog-sync/*` contracts remain the catalog service API.

| Approved scenario | Implementation | Evidence |
| --- | --- | --- |
| One Products section and toolbar | ProductsWorkspace, AppShell; overview/rules/history/transfer | redesign browser routes and removed-route assertion |
| Both providers in one list | `/api/products`: source-owned identities, mapped pairs, orphan MoySklad products/variants, missing confirmed observations | ProductWorkspaceIncludesBothSourcesAndMissingConfirmedObservation |
| Two import directions | ProductImportHub, ProductImport, existing Creation/Facts editors | paginated component test; real catalog browser create/readback/replay |
| Rules and mixed direction | existing field policies under `/products/rules`; `/products/transfer` reviews selected updates | real browser verifies one write to each provider and no-op after readback |
| History and unknown result | dedicated history table, focused dialog, existing read-only reconciliation | CatalogSyncOperations tests; real browser async polling/reload |
| Exact versus suggested matching | deterministic matching.Preview, ProductAutoMatching settings/preview/results | matching unit cases; scoped DB race/replay tests; 3 browser widths |
| Explicit opt-in after imports | versioned account-pair policy, worker cycle, immutable audit | StandingRuleWaitsForNewImportAndCanBeRevoked |
| Link a created identity | readback first, then complete imported observations and canonical mapping review transaction | CatalogCreatedLinkRequiresImportedIdentityAndPreservesConflicts |

## API and data contracts

All resources use the authenticated workspace and the exact MoySklad/Ozon
account pair. Merchant middleware rejects forged tenant bodies/query parameters;
mutation endpoints require same origin and CSRF. Store methods also recheck actor,
registered accounts and current evidence. No provider credentials enter these DTOs.

* `GET /api/products`: `provider=ozon|moysklad` or the combined list; existing
  search/archive/mapping filters and pagination. Every item now explicitly has
  `provider`, `kind`, `observed`, `shared_inventory`. No old DTO fallback remains.
* `GET /api/catalog-sync/products`: pinned import cursors; `summary` counts the
  entire matching catalog snapshot, not the page. `to_review` is selection
  eligibility, not an assertion that a provider will accept creation.
* `POST /api/catalog-sync/import-preview`: scope plus 1–20 `targets`, each with
  a source identity, empty destination identity and explicit `create.values`.
  Server derives a stable identity from tenant, account pair and source;
  browser-generated creation keys cannot bypass an existing attempt. Returns
  the existing preview DTO with per-item decisions and a ten-minute expiry.
* `POST /api/catalog-sync/approve`: existing preview ID, explicitly selected
  ready enrollment IDs and idempotency key. Approval atomically persists one
  operation and its outbox work. A one-time import does not enable, replace or
  enroll automatic field rules. Only an absent configuration is initialized to
  the disabled default. Existing revisions/rules/targets remain unchanged.
* `GET/POST /api/product-matching/settings`: policy `{revision, exact_sku,
  names, auto_confirm, after_import}`. Revision is optimistic; automatic
  confirmation and after-import execution default to false. Saving records an
  immutable policy revision, consenting actor/owner and activation import IDs.
* `POST /api/product-matching/preview`: scope and `expected_revision`. Persists
  the ordered plan/fingerprint for five minutes; never changes mappings.
* `POST /api/product-matching/runs`: scope, `preview_id`, 1–100 `product_ids`,
  `acknowledge:true`, and stable `idempotency_key`. One transaction revalidates
  policy, snapshot, actor and mappings under the same locks used by manual
  review/import. Returns durable per-item outcomes and confirmed count.
* `GET /api/product-matching/runs/{id}`: scoped durable result. A repeated POST
  with the identical request returns its stored result, including after the
  preview expires. Key reuse with different inputs conflicts.

Malformed input is rejected before planning; missing provider facts appear as
blocked item decisions, allowing an explicitly selected ready subset. Changed
revision/fingerprint, expired or incomplete evidence returns the existing 409
stale/conflict response. Missing or unauthorized account scopes return 404.
Uncertain external outcomes stay fenced and cannot authorize another create.
An interrupted matching response can be checked using the identical request/key;
the UI freezes its selection until that result is known.

Migration **70** adds nullable imported MoySklad lifecycle, scoped matching
policies/audits/previews/runs/cycles, and the created-link status ledger. Historic
NULL lifecycle is unknown, never active by default. Canonical product mappings,
confirmation audits and history keep their existing meaning and constraints;
shared inventory is retained. Rollback works before policy/run/link evidence
exists; afterwards it refuses to erase audit and requires a forward fix.

## Safety, execution and limits

Exact matching trims whitespace only. Case, punctuation and leading zeroes
remain significant. Names use Unicode tokens solely for manual suggestions.
Barcode matching is visibly unavailable because the imported shared data does
not contain independently verified compatible barcode evidence. Duplicate
articles/offers, variants, archive or unknown lifecycle and conflicting mappings
cannot auto-confirm. Existing mappings are never replaced by this workflow.

Both latest imported snapshots must be complete and at most one hour old.
Manual execution and automatic cycles reuse canonical mapping review and audit
transactions. Standing rules require a new import after activation, a still
valid owner and an unchanged revision. Cycles process at most 100 exact pairs;
all matching work is bounded to 2,000 products per source and 5,000 relevant
mappings. Larger snapshots fail closed rather than silently matching a prefix.
Name tokens are indexed once per plan. This is a deliberate initial limit;
large-catalog background partitioning is not implemented.

Creation uses existing adapter validation, durable dispatch/unknown-outcome
fences, Ozon async task polling and provider readback. No transaction spans an
external request. A verified create schedules read-only catalog refreshes;
mapping happens only after those exact identities appear in complete fresh
imports. Existing conflicting mappings are retained and shown for manual review.
If refresh fails, the link remains waiting and the user can refresh integrations.
The worker gives ordinary synchronization a turn even while links are waiting.

Ozon category/type requirements come from the existing runtime metadata and
preflight APIs. Prices, currency, VAT, dimensions, weight, images and required
attributes are explicit inputs; unsupported combinations stay blocked. Variants
are currently excluded from this batch creation flow, with a visible explanation.
Batch size is 20; selection survives pagination, and selection totals are clearly
distinct from whole-catalog counts.

Official references consulted: [MoySklad JSON API 1.2](https://dev.moysklad.ru/doc/api/remap/1.2/)
documents `syncId` replay semantics. The [Ozon Seller API documentation](https://docs.ozon.ru/api/seller/)
returned no readable API content in this environment and the swagger fetch was
closed by the remote endpoint. No claim of a newly downloaded complete Ozon
schema is made; runtime provider preflight remains mandatory and existing
adapter contract tests are retained.

## Verification record

Development stays on `codex/products-unified-workspace`; no commit or push has
been made. No production migration or provider write was run. The catalog browser
test rejects all non-loopback requests, uses generated fixture credentials, a
separate PostgreSQL schema and local provider servers. The regular test policy
also scrubs provider/production environment variables and provisions a disposable
database. Real external side effects are not part of acceptance here.

Verification completed before the user's request to stop heavy local runs:

* `make check-plan`: schema/shared contracts require the full CI lane.
* Frontend typecheck, ESLint, production build and **379/379** unit tests passed.
* `make check-ui`: **56/56** passed, including Products, import, matching,
  responsive layout, keyboard/focus and the removed-route assertion.
* Race-enabled Go checks for matching, catalogsync, postgres and worker passed;
  selected Go lint reported zero issues.
* Focused PostgreSQL scenarios passed: migration round trip/audit guard,
  whole-catalog counters, missing observations, batch approval, concurrent
  replay, manual invalidation, incomplete imports, revoked standing rules and
  created-link conflicts.
* `TestCatalogMerchantBrowserStack` passed after adding cross-tenant and missing
  CSRF assertions: **49.756 s**, real Go handlers/queue/adapters, loopback providers.
* `TestRedesignMatchingBrowser`, `TestWarehouseOverviewBrowser` (two Playwright
  scenarios), and `TestMoySkladSettingsBrowser` passed.
* Both controlled automatic browser scenarios passed after correcting the
  obsolete login calls, opening the current setup panel, and fixing mobile CSS.
* The dedicated stock-publication contract run passed its substantive cases;
  its subprocess-only crash helper is intentionally skipped in the parent and
  executed by its crash-recovery test. The separate shadow browser case failed
  before the mobile CSS correction and still needs a rerun.

The full `make check-full` run **did not pass**. It hit the integration package's
40-minute limit while another ordinary test was starting, and reported failures
before the timeout. The original three fixed-account unit expectations and two
obsolete UI assumption tests were reproduced on the clean baseline and fixed;
the focused repeats of those tests passed.

Additional fixes prepared from the broad run cover the provider quantity ceiling,
obsolete fixture constraint removal, connected-scope constructor validation,
Ozon-only ambiguity test filtering, unavailable foreign scopes, and rollback
history verification. Startup now requires migration **70** and checks the new
Products tables and lifecycle column. The attempt to verify these last fixes was
**stopped at the user's request**; they must be checked in CI. No unrelated
password-hashing or authentication-performance change is included.

The expanded integration setup had a real responsive regression: desktop grid
rules occurred after the mobile overrides. The overrides now follow the base
rules, and two regression cases (768/390 px) were added. These two new cases and
other final edits have not had a fresh complete UI/typecheck/lint/build run.

### Deferred verification and remaining older test failures

On 2026-11-06 the user explicitly requested no further heavy local tests because
of game performance, and deferred broad verification/further test repair to CI
and a separate task. All task-owned test processes and the disposable review
PostgreSQL container were stopped. Do not restart broad local checks merely to
complete this handoff.

* Re-run the full required CI lane; the timed-out run left later and deferred
  parallel integration tests unexecuted.
* Verify the additional Go fixes listed above; the latest focused run was stopped.
* Finish updating `TestMerchantBrowserWorkspaces` to the current connection
  screens and consent flow. Its account selector, product action selector and
  obsolete SQL fixture step have been corrected, but the whole journey is not
  green.
* Re-run/repair controlled one-time publication and unknown-result recovery
  browser scenarios. They now supply explicit evidence and select catalog-only
  Ozon loading, but the most recent completed runs failed at an old mapping
  confirmation selector and at publication readiness respectively. The selector
  was subsequently corrected; readiness still needs investigation.
* Re-run the shadow browser case after the mobile grid fix.
* Standalone browser scripts outside the executed harnesses were not exhaustively
  run; no claim that every repository test passes is made.

Logs from this task are preserved in `.local/products-workspace/checks/`.
`full.log` records the failed/timed-out run; `old-go-fixes.log` is the stopped
focused attempt. These are local diagnostic artifacts, not a successful CI run.

### Screenshots and delivery

Browser captures are in `.local/products-workspace/screenshots/`, including
`1440-products.png`, `390-products.png`, `1440-matching-preview.png`,
`390-matching-preview.png`, and the real catalog stack's creation/history views.
They were captured during the successful Products UI and catalog-stack checks;
minor final touch-target/semantic-colour changes may postdate an individual copy.

No commit, push, merge, production migration or real provider write was performed.
Unrelated `.agents/` and `skills-lock.json` additions were preserved.
