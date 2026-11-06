# Warehouse overview and stock detail

`/warehouses` is the common warehouse mapping view. `/warehouses/:warehouseId`
shows one warehouse and its source-specific stock observations. External IDs are
URL-encoded. Account changes reset the warehouse route, search, page and drawer;
query keys include the account pair. Returning from details preserves the list
query string.

## Read contracts

The existing independent-list contract of `GET /api/warehouses` is preserved.
Two optional views accept `tenant`, `moysklad_account`, `ozon_account`, `search`
and `offset`:

- `view=overview`: 25 Ozon warehouse rows, corresponding store names, mapping
  state, actual stock observation time and counts for the entire account pair.
  `filter` accepts `mapped`, `unmapped` and `attention`; `warehouse` selects an
  exact warehouse for a deep link. Search covers current warehouse/store names
  and accessible Ozon warehouse IDs, before pagination.
- `view=candidates`: 25 current MoySklad stores, with search across all stores and
  an availability flag. Occupied stores remain visible and disabled, without
  disclosing the other side of their mapping.

Offsets are bounded to 0–100000; search/IDs are bounded to 256 bytes. Ordering is
by external ID. `WarehouseOverview` also supports a bounded cursor/limit for the
existing orders consumer. `GET /api/orders/warehouses` adapts this same read model
while retaining its own configuration/authorization precondition and JSON shape.
No order dispatch or warehouse fallback logic changes.

Overview rows, counts, import-presence flags and pagination metadata come from
one SQL statement/snapshot. Counts cover the whole scope, independently of search
or selected status. The filtered total covers the search/status selection. A new
import between HTTP requests may legitimately change the next page or totals.

`GET /api/stock` keeps its existing fields and 50-row pages. Additive `name` and
`article` fields come from the same local import; `search` covers available
name/article/code/SKU/ID evidence. No provider requests or product images are
invented. Quantities remain decimal strings; NULL remains NULL, and missing rows
are never synthesized. Ozon exposes present/reserved/free_stock; MoySklad exposes
stock/reserve/incoming/quantity without a cross-source formula.

## Evidence and confirmation

Both account registrations must exist in the authenticated tenant. Queries retain
`registered_at`/`include_import_history` bounds and completed Ozon traversal.
Mappings require active, completely matching history visible under both account
registrations. Invisible history produces a conflict flag without mapping IDs or
store names. Missing current warehouse/type/store evidence and unsupported
capabilities are attention states, not confirmed mappings. rFBS/Express eligibility
uses the existing review policy, including warehouse type/state and scope policy;
there is no new deployment whitelist or publication permission.

The drawer performs read-only pair preview, durable review creation, inspection,
explicit consent and the existing idempotent confirmation. Its current-review
checks include both accounts, both warehouse IDs, review ID, import IDs, session
principal, expiry and absence of an existing mapping. Consent resets for each new
review; a local expiry timer disables confirmation. Failed inspection cannot enable
confirmation using cached success. Technical evidence/history is disclosed only
inside details, with history pagination preserved.

The app-owned confirmation intent survives closing the drawer and switching
accounts. An uncertain outcome retries the exact serialized body/key, never a new
review. Session loss prevents replay from another session. A malformed/mismatched
warehouse success response remains uncertain. Server success invalidates overview,
candidates, stock detail, orders warehouse settings and existing diagnostic caches.
Mapping does not enable stock publication.

Observation age uses the existing one-hour console window. Missing rows, invalid
or future times, old observations and received observations have distinct labels.
For overview aggregation, a future timestamp takes precedence; otherwise the
oldest observed row determines the warehouse's age. Refresh rereads local data.
No schema migration, new dependency or worker is required.

The warehouse table wraps long names inside its surface and reserves space for
status badges and actions. At widths up to 1100px, rows become labeled cards.
Search uses one outer focus indicator, including keyboard focus. Shared operation
feedback follows the current page content; integration feedback follows its page
content as well.

## Verification

Use `make check-plan`, `make check`, `make check-ui` and
`make check-integration` according to the repository policy. Warehouse Playwright
specs select the warehouse contract file instead of unrelated full regression.
The optional real-database browser acceptance reuses the existing Vite/Playwright
fixture harness and a synthetic local session:

```sh
TEST_DATABASE_URL='postgres://postgres@127.0.0.1:PORT/styk_orders_store_test?sslmode=disable' \
STYK_WAREHOUSE_BROWSER_E2E=1 \
go test -race -tags=integration ./tests/integration \
  -run '^TestWarehouseOverviewBrowser$' -count=1 -timeout=4m -v
```

Run against an isolated migrated test database. The normal selected integration
lane skips this optional browser test; run it explicitly for warehouse UI changes.
It seeds observations, uses no external providers, checks search/pages, occupied
stores, stale reviews, explicit consent, lost responses/exact replay, concurrent
mapping conflict, unsupported/missing evidence, order visibility, separate source
stock fields, zero vs unknown, direct links, refresh, Back/Forward, mobile layout
and focus restoration. The presentation lane covers account switching and layouts
at 390, 768, 1280, 1366, 1440 and 1920 pixels. Warehouse layout checks additionally
cover long imported names, the 1100/1101px breakpoint, the search focus indicator,
source-card spacing and operation feedback after warehouse, orders and overview
content.
