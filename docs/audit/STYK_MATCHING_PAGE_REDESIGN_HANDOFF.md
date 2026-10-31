# STYK matching page redesign

Branch: `feature/styk-product-matching-redesign`.
PR: https://github.com/lilter96/styk/pull/3 (no merge).

## Scope

Redesigned the existing `/products` page, reusing `ProductReview` and the existing
review/confirmation/idempotency/audit workflow. No synchronization, provider,
worker, authorization, conflict, mapping-version or audit-write logic changed.
No migrations, seed changes, external writes or production mocks were added.

Removed the Integrations back link. On this page the global confirmation notice
uses a compact presentation: historical success timestamp, mapping ID/version,
evidence code and technical disclosure no longer occupy the workspace. Pending,
uncertain-outcome, error and session-loss recovery actions remain available.
These changes affect presentation only; stored confirmations and audit survive.

## Changed files and components

- `web/src/ProductMappingPage.tsx`: page composition, real progress summary,
  debounced server search, supported filters/reset, pagination and safe queue.
- `web/src/ProductMappingTable.tsx`: row layout, placeholders, selector opening
  the existing modal, copy control, independent archive/mapping badges, actions,
  current-page selection and pagination components.
- `web/src/matching-page.css`: scoped foundations, fixed table columns, 56px
  placeholders, compact rows, sticky headings and scrollable table region.
- `web/src/App.tsx`, `web/src/Confirmation.tsx`: route-scoped compact notices.
- `web/src/App.test.tsx`, `web/src/MappingWorkflow.test.tsx`: assertions updated
  for the new badge text and page-selection checkbox, retaining safety checks.
- `internal/console/products.go`: exact filtered total and variable page size.
- `internal/console/product_pagination.go`, `product_pagination_test.go`:
  bounded pagination parsing and regression cases.
- `web/e2e/matching-page.spec.ts`: local Go API and browser regression coverage.

Unrelated earlier overview changes remain outside these commits.

## Read-only API changes

`GET /api/products` keeps `tenant`, `moysklad_account`, `ozon_account`, `search`,
`archived`, `mapping`, and `offset`. Legacy callers still receive 50 rows by
default. Optional `pageSize` accepts 1–100; the page UI offers 20/50/100. Optional
`page` is 1-based and overrides offset. Maximum offset remains 100000; invalid
sizes/pages return HTTP 400 `INVALID_PAGE`.

Response preserves `items`, `has_next`, `offset` and adds `total`, `page`,
`pageSize`. `total` is the count after the same SQL search/archive/mapping filters,
including on empty or out-of-range pages. Count and page are read within the
existing read-only repeatable-read transaction. Both queries share one filter
CTE; stable ordering remains product ID. Page-size changes do not change matching
rules. The matching engine still processes only the bounded returned page.

No full catalog is fetched for frontend filtering or pagination. Local filtering
is limited to UI selection/queue IDs. Search consumes React Query's abort signal
and uses a 350ms debounce; changing filters/search/size resets the page. The
initial debounce no longer resets a page after navigation.

## Data and behavior

Progress uses current-catalog counts from diagnostics, validated before computing
percentage and remaining count. Confirmation invalidates products and diagnostics
as before. Historical mappings are never used as a current-catalog denominator.

Archive is independent from matching status. Confirmed maps to green, conflict to
red, and other server matching outcomes to an orange unmatched presentation; the
underlying server status remains in the badge tooltip. Archive gets its own badge.

The read contract currently provides no product image URLs or MoySklad SKU.
Neutral placeholders are shown, and no Ozon SKU is mislabeled as a MoySklad SKU.
MoySklad name/ID/article use the mapped values returned by the existing endpoint;
the former extra first-page MoySklad lookup is removed. Full identifiers remain
in tooltips; the mapped article can be copied.

Only supported filters appear: archive/activity and confirmed/unconfirmed
mapping membership. Conflict and arbitrary product-status filters are not added:
their exact engine-derived semantics are not supported by the SQL page selector.
There is no decorative status selector or new client-side classification filter.

Mass selection is explicitly limited to eligible suggestions on the current page,
with the existing 100-item queue cap and individual review/acknowledgement. The
queue persists across pages; deselecting a page removes only its eligible IDs.
Confirmed, archived, truncated or candidate-free products cannot be batch-queued.
Queue capacity does not block opening a row individually. Completed queues cannot
trigger an inert bulk action. Save-and-next remains the existing modal workflow.

## Validation

- Frontend typecheck/production build, ESLint and existing Vitest suite pass.
- An isolated clean checkout of the committed branch also passes build, lint and
  all 203 committed frontend tests, without the unrelated uncommitted overview.
- Go: `make lint` (go vet and golangci-lint), `make test`, `go test -race ./...`,
  `make build`, `go build ./...` pass in the existing Go 1.27.3 Docker environment.
- Four real local Playwright tests pass: general application access, anonymous
  denial, matching modal confirmation/save-next, and the new page scenario.
- Page test covers positive name/offer/SKU/ID/article search, exact total, 20/100 sizes, server page navigation, invalid
  pagination, empty/deep pages, membership totals, archive filtering, foreign
  tenant rejection, clear/reset, current-page selection, clipboard copy, opening
  an existing link, bulk queue opening and console errors.
- Screenshots inspected at 1536×1024, 1920×1080, 1440×900, 1366×768; tablet/mobile
  screenshots at 820×1180 and 390×844 additionally assert no document overflow.
- The old modal scenario continues exercising real confirmation and advancing
  to the next product against the updated local Go API. Only isolated seeded
  local STYK mappings are written; no provider mutations occur.

The local API binary was rebuilt and restarted with existing DPAPI-protected demo
configuration. Its prior binary is preserved at `.local/matching-page-backup-api.exe`.

## Visual evidence and limitations

- [Before](matching-page/before.png)
- [1536×1024](matching-page/1536.png)
- [1920×1080](matching-page/1920.png)
- [1440×900](matching-page/1440.png)
- [1366×768](matching-page/1366.png)
- [Selected/unmatched](matching-page/selected.png)
- [Empty search](matching-page/empty.png)
- [Tablet](matching-page/820.png)
- [Mobile](matching-page/390.png)

The approved reference screenshot was not attached in the available conversation.
Consequently no pixel-equivalence claim or fabricated visual comparison is made.
The written composition/tokens and actual before/after renders guided this pass.
The browser review corrected table height/footer visibility, pagination styles,
and the initial debounce race. PR remains draft pending reference comparison and
owner acceptance. Large production-catalog performance has not been benchmarked;
the existing database timeout remains enforced. No indexes were added without
query-plan evidence.
