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

## Spacing and interaction audit after browser comments

This audit corrects defects missed by the earlier rendering review. The user's
13 comments exposed cascade, sizing and scroll problems; the earlier screen
coverage list did not establish correct component geometry.

Shared CSS now loads before the Products styles, explicitly in `main.tsx`.
Products rules are scoped to the workspace, matching drawers have a single
layout stylesheet, and controls share a styled native picker. The mandatory
project `styk-ui-workflow` takes precedence over the implementation guidance
added to `AGENTS.md` and `docs/frontend-styling.md`.

| User comments | Correction and observed result |
| --- | --- |
| 1–2, detail spacing | Cards have 20 px padding and 16 px gaps; permission rows have 12×16 px padding. Mapping status and product identity form one padded group. Detail content scrolls independently of the action footer. |
| 3, reverse matching | Structured source, useful empty state and clear-search action, conditional pagination, selected-product hint and explicit review action. Long mobile names use a full row below the icon/status. |
| 4, source composition | Provider/status, name and offer are grouped; secondary identifiers are in a disclosure. |
| 5–6, overlap | Bounded results cannot flex-shrink; pager and expanded supplementary information follow in normal flow. Measured list→pager and panel→disclosure gaps were 16 px. |
| 7, 11, dropdowns | Styled native closed/open/selected/focus/disabled states; provider icons centered on control height. The additional compact-desktop report revealed a 160 px winning flex rule: the source filter now reserves 200 px. All three values had `scrollWidth === clientWidth` at 1366 px; the arrow remained visible. Mobile source/lifecycle filters each use the full available row. |
| 8, automation | Table owns its scroll region and 12 px padding; separate pager has 16 px top spacing. Mobile permissions retain field labels and select-all. |
| 9–10, matching rules | Explicit grid layout, padded signal groups and separate confirmation choices; after-import rule stays within the section. |
| 12–13, rules | Padded direction presets; explicit Inter 14 px/600 switch label; mobile presets fit the parent. |

Additional findings fixed during the walkthrough: stretched price inputs next
to currency help text, category search alignment, collapsed primary category
content, duplicate category summaries, cramped unknown-result actions, and a
desktop minimum width left on mobile history cards. The reverse empty-state
action also resets pagination; an empty later page retains a way back.
The sidebar account pickers were also checked: the MoySklad picker now uses a
full row; both supplied account names and their arrows fit without overflow.
Table headings wrap at the default compact desktop width instead of clipping.
Following the additional long-name comment, overview names are clamped to two
lines with the same reserved height for short names; article codes use one line.
Full values remain in tooltips and the product drawer. All six desktop fixture
rows measured 92.5 px, including the long-name row; all six mobile cards measured
157.5 px at 390 px width.

Applied skills: official `frontend-design`, plus `design-system`,
`design-critique`, `accessibility-review`, `ux-copy` and `design-handoff` for the
affected controls, copy and documented patterns. No new user research was needed
for these concrete corrections. This is a focused review, not a full
accessibility certification or screen-reader test.

Visible in-app browser review used the reported 1744 px width with a short
1000 px viewport, 1366×768, 1440×900 and 390×844. Inspected routes and nested states:

- `/products`: source/lifecycle filters, all source values, overview cards,
  manual forward matching, reverse matching with long names, empty/results
  states, pagination and expanded additional information; all four detail tabs.
- `/products/rules` and `/products/rules/automation`: source/interval pickers,
  direction presets, field controls, table end/pager and bulk action panel.
- `/products/matching`: all three settings groups and preliminary results.
- `/products/import` and both direction routes: selection, Ozon editor steps,
  expanded category/factual fields, readiness and final confirmation.
- `/products/history` and `/products/transfer?operation=...`: history cards,
  processing operation and unknown-result drawer.

The initial 15543 review used the actual local API and existing data. Final
responsive/rare-state review used the separately marked in-memory demo on 15545;
it does not prove provider or database behavior. Later, the actual local session
expired and the stored local login returned HTTP 401. The final repeated API
walkthrough therefore remains unverified; no credentials were reset or data
changed to bypass it. No provider writes, workers or migrations were started.
Screenshots are in `.local/products-spacing-review/` (ignored by Git).

Keyboard observations: opening the native picker with Space and its visible
focus outline; Escape closes the detail drawer and returns focus to the opening
product button. Contrast was measured for the changed permission text and then
darkened where the initial measurement was below 4.5:1. Screen-reader behavior,
browser engines without customizable selects and a full zoom audit were not run.
Final permission/status text measurements were 5.29:1, 5.10:1 and 5.51:1;
the detail body ended exactly at the action footer's top edge with no overlap.

Verification: `make check-plan`, frontend lint, TypeScript/build, formatting and
`git diff --check`. The user explicitly prohibited local test suites: no unit,
UI/E2E, integration, policy self-test or full regression suite ran for this audit.
The planner does not require full regression for this styling follow-up. The
earlier backend contract CI gate described above is separate and remains open.
