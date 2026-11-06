# Product mapping conflict resolution

## User task and interaction contract

`Разобраться` explains the contradiction before asking the operator to change
anything. The product row shows its actual conflict reason; shared stock is
supplementary information, not an error reason.

A full-page workbench and a drawer with the entire searchable catalogue were
considered against a staged drawer. The staged approach keeps the product list
in context and makes the reason/comparison visible before search or persistence.

The drawer opens with a reason, the current Ozon/MoySklad pair and matching
article/code candidates. Selecting a candidate opens a comparison. Catalogue
search and version history load only when requested. Internal IDs stay in the
history disclosure.

| State | User action and result |
| --- | --- |
| Diagnosis | Compare a candidate, find another product, inspect history or refresh. No write occurs. |
| Search | A debounced query searches loaded MoySklad products. Ten results per page; choosing one opens comparison. Empty results offer clearing the query. |
| Comparison | Show both identities and the current/new target. Nonmatching article/code requires extra attention. |
| Preflight | `Проверить замену` creates a session-bound review of exact import and mapping versions. It does not change a mapping. |
| Eligible review | Focus moves to an explicit acknowledgement. Only acknowledgement enables `Заменить связь`. |
| Saving | Prevent another mutation; keep the immutable request and idempotency key. Closing the drawer does not cancel a submitted request. |
| Unknown outcome | `Уточнить результат` repeats the same `/replace` request and key. Never silently issue another key. |
| Rejected/stale | Explain the blocker; refresh and create a new review after resolving it. Active or unknown transfers must finish/reconcile first. |
| Success | Confirm the saved target and retained history. No provider write occurred. |

An account mismatch asks the operator to select the original account. An occupied
offer asks for correction in Ozon and catalogue refresh. Neither offers a target
replacement. A changed Ozon article allows reviewing the current target with the
new article. A no-op replacement is rejected by the server.

## Visual and accessibility contract

The existing STYK shell and brand tokens remain authoritative. `products-conflict.css`
owns this component's geometry and loads explicitly after shared controls in
`main.tsx`; it does not redefine shared brand or shell tokens.

- Desktop drawer: at most 700 px, full viewport height; header and footer do not
  scroll. The body owns overflow, 24 px padding and 24 px section gaps.
  Its children must not flex-shrink: an overflow-hidden identity card can otherwise
  collapse to a border on a short viewport despite having correct internal padding.
- At 600 px and below: full width, stacked identities, 16 px horizontal body
  padding, 20 px gaps and a full-width primary footer action.
- Inter/system UI type, 24 px main section heading (21 px on mobile), 14 px body,
  12–13 px metadata. Interactive targets are at least 44 px tall.
- Reuse `--styk-ink`, primary, line and semantic status tokens. The component's
  secondary text mixes 90% `--styk-muted` with 10% ink to retain readable contrast
  on the shared pale background. Do not change the application palette by route.
- Search results clamp names to two lines and truncate metadata; full values
  remain in accessible text/title and comparison. Comparison identities wrap.
- Pagination follows results in normal flow. Keep prior results disabled while
  the next page loads; focus the first result after pagination, not while typing.
- The named modal uses `IntegrationDialog` focus containment, Escape and return
  behavior. Views focus their heading or search input; preflight focuses the
  acknowledgement. Loading and result messages use status/alert semantics.

## API and persistence

`GET /api/products` adds `conflict_reasons` (`OTHER_ACCOUNT`, `OFFER_CHANGED`,
`DIFFERENT_CANDIDATE`, `OFFER_CONFLICT`). The conflict filter counts and paginates
candidate mismatches as well as account/offer contradictions in SQL.

`POST /api/product-mapping-reviews/{id}/replace` accepts the same body as ordinary
confirmation: `idempotency_key`, `acknowledge`, optional `explanation`.
The server selects replacement mode; clients cannot set session/scope/target in
the confirmation request. Review ownership, merchant authorization, origin/CSRF,
expiry, import fingerprints and the exact existing mapping version are rechecked.
The normal `/confirm` endpoint still refuses to replace any mapping.

A replacement keeps the mapping ID and appends version N+1, immutable history,
the review-bound audit and idempotent outcome in one transaction. The fingerprint
binds replacement mode, so reusing the same key on `/confirm` cannot change the
operation. Already committed outcomes remain replayable after review expiry.

The replacement transaction shares source/matching advisory locks with existing
dispatch guards and takes bounded table locks on catalog/order/stock work during
the active-work check and write. Only work involving the affected mapping/product
blocks the replacement (`409 MAPPING_BUSY`); no work is cancelled. The table locks
briefly serialize other work mutations, bounded by the store timeout. Existing
frozen grants/configurations retain their old mapping version and must pass their
normal freshness checks again; replacement does not silently renew consent.

`ReplacementAllowed` on review responses means the reviewed pair is replaceable;
it is not a promise that active work will remain idle until submission.

Migration `00071_product_mapping_replacement.sql` adds immutable replacement intents
and a deferred FK requiring the complete confirmation audit at commit. Reviewed
mapping deletion/direct unreviewed updates remain forbidden. Down restores the
old guard only when there are no replacements; after a real replacement, use a
forward correction to preserve history. Startup requires both schema 71 and the
replacement table, preventing a different migration 71 from appearing compatible.

## Migration 71 collision on the development stand, 2026-11-06

The shared stand had PR #6's `00071_marketplace_plans.sql` from commit
`309c35e703e4e434b16d120aca6b6189af065f02`. At the user's explicit instruction, its
schema was rolled back and this feature's migration 71 applied atomically.
Before rollback, all seven workspaces had plan revision 1 and there were no
`MARKETPLACE_PLAN_CHANGED` events. One automatically assigned plan_3 prevented
the original Down guard; original assignments were saved privately before
normalizing those initial assignments inside the rollback transaction.

Private recovery material is ignored by Git under
`.local/products-conflict-review/`: the exact foreign SQL and
`main-marketplace-plans-backup.json`. Do not commit workspace identities or runtime
credentials. The original SQL is also available on PR #6's branch. On integrating
that PR, renumber its migration to the next free version (expected 72), review the
saved initial assignments and restore them as appropriate. Do not apply two
different migrations under version 71 or remove this feature's history table.

## Verification record

Manual checks and build results are recorded for this change; a browser screenshot
does not establish provider integration or full accessibility compliance.

- Isolated Neon copy: foreign-71 rollback → our Up → Down → Up succeeded. A
  replacement through the actual browser/API saved version 2, preserved version 1,
  and persisted the corresponding audit and retry outcome. No worker was started.
- Shared stand: our schema 71 present, foreign plan columns absent, zero mapping
  replacements. The real conflicting pair remains unchanged for owner review.
- Browser `/products`: desktop diagnosis at 1440×900; expanded history and
  keyboard disclosure/focus at 1366×768; mobile empty search and pagination with
  long identifiers at 390×844; comparison/preflight at 390×640; successful isolated
  replacement at 2036×1257. Escape returned focus to the initiating row action;
  forward/reverse Tab wrapped inside the dialog.
- Body/footer geometry was measured: their boundaries meet without overlap;
  the drawer has no horizontal overflow at the checked widths. The short mobile
  comparison card retained its full 259 px height after fixing flex shrink.
  Secondary text measured 5.07:1 against the drawer background (previously 4.45:1).
- Passed: frontend TypeScript, ESLint, Vite production build; serial Go API and
  worker builds; `go vet -p 1` for console/postgres/domain. Vite retained its warning
  about a JavaScript chunk larger than 500 kB. `git diff --check` passed.
- Added Go unit, PostgreSQL contract and React interaction coverage for semantic
  fingerprints, replacement eligibility, preserved history, replay, concurrent
  reviews, rollback, pending publication, access guards and lazy UI behavior.
  These test suites were **not run locally at the user's request**.
- `make check-plan` selects full CI because this changes schema/shared contracts.
  Do not substitute manual inspection or a build for that outstanding CI lane.
  Error/unknown/stale/other-account states are implemented; unknown outcome and
  account mismatch have authored component coverage. Only the states explicitly
  listed above were exercised manually. A screen reader audit was not run.
- Screenshots are private local evidence in `.local/products-conflict-review/`:
  `conflict-desktop.png`, `conflict-mobile.png`, `conflict-final.png` and
  `replacement-success.png`. Research/interview skills were unnecessary: the
  scope is grounded in the owner's reported task and observed interface, with
  no claims of user research. Applied UI skills: frontend-design, design-system,
  design-critique, accessibility-review, ux-copy and design-handoff.
