# STYK product matching handoff

Branch: `feature/styk-product-matching-redesign`.
Base: `chore/styk-cleanup-and-demo-seed`, commit `6adf17c`.

## Scope and acceptance status

The working application now has a wide two-column matching dialog, server-side
catalog search/count/pagination, selection, copying identifiers, retained review
and audit workflow, and save/skip/advance controls. This is **not a claim of full
acceptance**: no reference screenshot was attached to the request, and the
persisted MoySklad catalog does not contain separate SKU or active/archive fields.
Those controls are unavailable rather than implemented using fabricated values.
Images and prices are also absent from the current read contract; placeholders
and omitted prices are intentional. No production mocks were added.

## Files

Frontend:
- `web/src/ProductReview.tsx`: existing review controller and retained evidence,
  history, conflict and uncertain-outcome controls, new composition and footer.
- `web/src/ProductMatchingPanels.tsx`: Ozon identity panel, clipboard actions,
  search/filter/results/pagination views, keyboard result navigation.
- `web/src/product-matching.css`: scoped responsive layout and states.
- `web/src/ProductMappingPage.tsx`: bounded, cancellable traversal of the existing
  server-filtered unconfirmed queue, skip tracking and completion message.
- `web/src/IntegrationDialog.tsx`: optional initial focus selector; existing focus
  trap, Escape and focus restoration retained.
- `web/src/Confirmation.tsx`: refresh search metadata after confirmed mutations.
- `web/src/main.tsx`: scoped stylesheet after application foundations.
- `web/e2e/product-matching.spec.ts`: real local API and browser verification.

Go:
- `internal/console/review.go`: extends the existing protected search route.
- `internal/console/target_search.go`: validated search/page/filter parameters.
- `internal/console/target_search_test.go`: bounds and invalid-input tests.
- `internal/postgres/mapping_search.go`: parameterized scoped SQL search/count.

Earlier uncommitted overview changes and design screenshots are unrelated and
are excluded from matching commits.

## Search contract

`GET /api/moysklad-products` retains cookie authorization and existing scope
normalization/registration validation. Parameters:

| Parameter | Behavior |
| --- | --- |
| `tenant`, `moysklad_account`, `ozon_account` | Existing authorized account pair |
| `query` | Trimmed literal substring, maximum 100 bytes |
| `searchBy` | `all`, `name`, `article`, `id`, `code`; `sku` returns 422 |
| `activeOnly` | Boolean; true returns 422 because activity is not imported |
| `inStockOnly` | Boolean; SQL requires observed aggregate quantity > 0 |
| `page` | 1-based, offset bounded to 100000 |
| `pageSize` | 1–100, default 50; dialog default 20 |
| `search`, `offset` | Legacy aliases retained for existing consumers |

Response: `items`, `total`, `page`, `pageSize`, `offset`, `has_next`,
`hasNextPage`. Items retain `kind`, `id`, `name`, `article`, `code`, `parent_id`,
and add nullable `quantity` and boolean `mapped`. Code is not relabeled SKU.
Unknown quantities remain unknown. `FIELD_NOT_IMPORTED` uses HTTP 422;
invalid inputs use 400; unavailable/unregistered scopes retain 404 and 503.

All constraints apply in SQL before LIMIT/OFFSET. A single SQL statement produces
the page and total from one snapshot, including empty pages. Stable ordering is
`kind,id`. Source imports respect registration history boundaries. Mapping lookup
is scoped by tenant, MoySklad account and selected Ozon account. No provider API
call is issued on typing. No migration or new indexes are introduced; substring
search scans the selected import, bounded by the existing database timeout.
Large-catalog query-plan/performance analysis remains a follow-up.

## Confirmation and queue behavior

Existing create-review and confirm endpoints remain authoritative. An operator
must inspect the pair and explicitly acknowledge identity before confirming.
The footer's first Match action creates a review if needed; it does not silently
substitute identity acknowledgement. Review expiry, stale evidence, conflicts,
idempotent replay, failure and unknown outcomes retain their existing controls.
This extra review step is an intentional departure from a one-click workflow.

`mapped` is informational: existing many-to-one mapping rules are not tightened
or relaxed client-side. Existing source-to-target conflicts are still blocked by
the review/confirmation backend. Confirmed mappings are never deleted or edited
directly by the new UI.

Save-and-next advances only when the exact review has a successful confirmation
result. Skip only changes queue UI state. The queue uses `/api/products` with
`mapping=unconfirmed&archived=false` and bounded pages of 50; it never loads the
MoySklad catalog in full. Skipped IDs are remembered until workspace/account
change or page reload. End-of-queue displays a completion message.

## Verification

- Frontend build, ESLint and all 206 existing Vitest tests passed.
- `make lint` (including golangci-lint), `make test`, `go test -race ./...`,
  `make build`, `go build ./...` run in the existing Go 1.27.3 Docker environment.
- Real local Playwright smoke passed against the updated Go API and isolated
  seeded PostgreSQL database. Tests cover name/article/ID/code search, total and
  page boundaries, in-stock filter, unsupported field responses, invalid size,
  foreign tenant rejection, selection, empty results, real review/confirmation,
  skip and save-and-next with search focus, and Escape.
- Screenshots inspected at 1920×1080, 1440×900 and 1366×768. Fixed conflicting
  global styles and initial-focus ordering found during browser verification.
- Only seeded local STYK mappings were written. No provider mutations or
  synchronization changes were performed. The local demo API binary was rebuilt
  and restarted; the previous binary is preserved in `.local/matching-backup-api.exe`.

Not fully exercised by the new browser test: whole-queue exhaustion, every
uncertain network outcome, browser clipboard permissions, production-sized query
plans, and all filter combinations. Existing workflow tests continue covering
rejected reviews, conflicts and lost confirmation responses. SKU/activity tests
verify explicit unsupported responses, not successful search/filtering.

## Visual evidence

All screenshots contain isolated seeded data:

- [Before](product-matching/before.png)
- [Normal](product-matching/normal.png)
- [Selected](product-matching/selected.png)
- [Search](product-matching/search.png)
- [Empty](product-matching/empty.png)
- [Existing mapping](product-matching/existing.png)
- [1366×768](product-matching/1366.png)
- [1440×900](product-matching/1440.png)

Pixel comparison against the intended reference remains blocked by the missing
attachment. The implementation follows the written dimensions and composition.

## Review delivery

GitHub CLI initially reported no authenticated host. The existing Git credential
helper successfully authorized pushing and PR creation, without exposing its
credential output. [PR #3](https://github.com/lilter96/styk/pull/3) is a draft
against the cleanup branch because reference-image and missing-field acceptance
work remains. The feature branch is published. No merge was performed.
