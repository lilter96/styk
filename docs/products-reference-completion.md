# Products: reference completion, 2026-11-06

Base: `origin/master` / `00ff2cf8cb0c8580e9cbb3a394324fff3b2e1d55`.
`git fetch origin master` and the fast-forward check completed before editing.
Final base also includes `7e485be` (the newer master documentation/dev-environment update).
This follow-up replaces the incomplete presentation in `44666fe`.
The approved archive is `STYK-products-approved-refs-v3.zip`; v3 takes precedence.

## Reference coverage

| Approved screen | Implementation |
| --- | --- |
| v3 overview; v2 overview | Shared account-pair header and factual summary, one toolbar, import dropdown, source/lifecycle filters, All/Linked/Unpaired/Conflicts pills, paired rows, selection → transfer |
| v2 rules | Source/destination field table, direction presets, master switch, interval, read-only limitations, automation entry, reset/save and revision guard |
| v2 automation | `/products/rules/automation`: canonical linked identities plus disabled unpaired rows, field permissions, per-product switch, state column, selection and bulk assignment, consent/save in the side panel; disabling remains possible when publishing is unavailable |
| v2 detail | Right drawer with Overview, Matching, Data transfer and History. Imported field comparisons, current policy, shared-inventory information and explicit actions |
| v2 matching | Right drawer, candidates, search, side-by-side names/articles, canonical server review and separate confirmation |
| v3 automatch review | Proposed candidates are selectable without inventing exact evidence; the canonical review still validates the chosen pair |
| v3 import hub | Two direction cards, account pair, server aggregates, step sequence and history entry |
| v3 MoySklad → Ozon | Separate selection, server-preflight readiness, explicit ready subset, final confirmation; blocked items remain editable |
| v2 Ozon import | Same server-authoritative staged batch flow; linked, archived, variant and unknown-lifecycle records cannot silently create |
| v2 create | Ozon editor steps: Basic, Price/dimensions, Category, Review. Existing category metadata and attributes, explicit factual values, draft retention |
| v2 preview | Per-product changes and blockers, selection summary, separate approval panel and acknowledgement |
| v2 transfer | Durable operation URL `/products/transfer?operation=...`, polling actual work states, details and history |
| v2 history | Per-product rows, direction, timestamp, actual outcome, full-history server search/status filters, counts and cursor pagination |
| v2 unknown | Dedicated result drawer, uncertainty timeline and guarded existing reconciliation; no invented retry or second write |
| v3 matching settings/results | Numbered policy sections, opt-in, exact/suggestion distinction, result counts/table, manual review and immutable run replay |
| v3 mobile; supersedes v2 mobile | STYK header, bottom navigation, compact cards and summary, reachable import/matching/filter actions, product subpages in More, full-width mobile drawers and steps |

Reverse manual matching is functional: starting from a MoySklad-only product
searches scoped Ozon products and passes the chosen source/target into the same
canonical review. A product name match never directly writes a mapping.

Creation still uses the existing provider preflight, approval, outbox,
idempotency and readback paths. Nothing in this follow-up adds a provider write
or applies a migration. Unknown outcomes continue to block another create.
The removed `/catalog-sync` UI route remains absent; no redirect was added.

## Contract changes

`GET /api/catalog-sync/operations` now supports `search`, `state`,
`product_provider` and `product_id`. Search matches operation IDs, imported names, source articles
and source/target offers. Filters apply to the same work row before cursor pagination; an
operation containing several products cannot expose unrelated rows in a product
history. Cursors bind all filters and the account pair. Summary counts cover the
matching search/product across the full history, independently of the selected
status. Page rows and counts are read in one read-only repeatable-read transaction.

`GET /api/products?mapping=conflict` filters existing foreign-account mapping
conflicts without returning foreign target details. Other product filters retain
their meaning. There is no schema migration in this follow-up.

Overview/card selection now carries the original account pair into the transfer
screen; switching account pairs cannot reuse that selection. The selected
identities are resolved again through the scoped server catalog before preview.

## Validation and delivery status

Passed on this follow-up:

- `make check-plan` (selection only; no tests started).
- Frontend `npm run typecheck`, `npm run lint`, `npm run build`.
- `GOMAXPROCS=2 go build -p 1 ./internal/console` and a bounded build of `./cmd/api`.
- `git diff --check`.
- Serial Chromium visual review at 1440 and 390 px: overview, rules, automation,
  import hub, preflight, final approval, all four detail tabs, matching settings
  and results, manual review, Ozon editor, unknown outcome, history and operation.
  No page JavaScript errors or horizontal page overflow in that review.

The initial visual-review data is explicitly synthetic and intercepted at the API boundary;
it is evidence of rendering/interactions, not live-provider or database acceptance.
The review server loads the real React application with no application API proxy,
and closes its owned browser/server afterwards. Existing user services are preserved.
Screenshots and scripts: `.local/products-v3-review/` (local, ignored by Git).

The user's restriction on heavy local tests is still in effect. **No unit,
integration, full regression or provider test suites were run in this follow-up.**
Prior passes from the earlier implementation do not verify this diff. Relevant
existing component/E2E expectations were ported to the staged import and separate
automation page. New contract tests cover history filters/scope/cursor binding and
the conflict filter; execution is deferred to CI. The planner requires the full
CI lane because the change touches shared catalog contracts and routing.

Additional in-app browser review used the current React app and a separately built
current Go API against the existing local development database. The original
5173/8080 services were preserved. The review preview remains available at `http://127.0.0.1:15543/products`
(API: 15544), with
no worker, migrations or provider writes. Existing merchant authentication was
used; credentials were not changed. Automation consent was checked as a local
form draft and then reset without saving.

Observed on the current API: exact product history returns both its reconciled
and blocked work; the reconciled count stays one when filtering to the blocked
state; full-history article search also finds the older blocked work outside the
first unfiltered page; cursor navigation to the older page completed successfully.
These are bounded manual read checks, not a passing
integration suite. Real-data screenshots are named `live-*.png` in the review
folder and are distinct from intercepted fixture screenshots.

The complete SQL history contract still requires its dedicated-database CI tests,
including cursor/scope rejection and mixed-operation coverage. Full regression is
an outstanding CI gate. No claim of production acceptance is made from screenshots.

Deliberate data/capability limits remain visible: screenshots do not supply real
product photos, barcode matching is unavailable until verified shared observations
exist, and unsupported provider fields stay read-only. No sample prices, category
facts or images from the references are injected into merchant products.
