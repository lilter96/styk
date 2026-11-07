# Automatic matching: one criterion, one catalogue-wide action

This replaces the paged matching workflow described in the November 7 follow-up.
Import batching and persistent creation drafts are unchanged.

## Interaction and API

`/products/matching` automatically searches when a criterion is chosen. Three
examples explain the result; the single primary action approves **every eligible
pair in both complete snapshots**. Exceptions are a collapsed summary with sample
links to manual review. Existing mappings are never replaced. Saved after-import
automation is a separate disclosure with its own explicit Save action.

- `POST /api/product-matching/jobs`: scope, `criterion`, `idempotency_key`.
  Returns complete global counts and at most three examples/exception samples.
  A repeated key returns the same complete job; changing its criterion is 409.
- `GET /api/product-matching/jobs/{id}`: the initiating actor's durable result.
  The response has no result pages or per-page approval.
- `POST /api/product-matching/jobs/{id}/apply`: scope, `expected_count`,
  `acknowledge: true`, and an idempotency key. The exact count belongs to the
  frozen whole-catalogue job, not a submitted product-ID list. A completed response
  contains the committed confirmed/skipped counts; UI success uses that receipt.
- Approval is durably admitted before application. A worker can recover an
  accepted request after interruption. Same-key retries return the stored result;
  different keys/counts conflict. Failed applications can be explicitly retried;
  expired imports, changed snapshots or revoked access require a fresh search.

The browser saves the criterion, job and uncertain request key before submission.
It never reports success from an optimistic update or a queued receipt. Choosing a
criterion or making a one-off match does not save an after-import policy.

## One native pipeline, at every size

There is no product-count cutoff and no alternate small/large catalogue engine.
All current criteria and the after-import rule use `product_matching_rows`,
immutable frozen candidates and the same atomic application statement. The Go
process receives counts and samples, not the catalogue. Adding a criterion must
extend its key projection and criterion registry, reuse this classifier and
application pipeline, and add classification/concurrency/performance contracts.
Do not add a per-product API/transaction loop or a size-dependent fallback.

Native set joins account for all duplicate keys, existing links, lifecycle and
variant restrictions. Imported source rows are immutable; application pins the
same complete imports and checks freshness/authorization again. Only still-exact
pairs with the same frozen target enter the write. Newly matched items are
skipped; newly discovered items require a new search.

Reviews, source fingerprints, canonical observation hashes, mappings, mapping
history, confirmation audits, idempotency receipts and the immutable job result
commit together. Any persistence failure rolls back the entire application,
while retaining its approved request and recoverable failure state. Deferred audit
constraints, foreign keys and actor lineage locks remain enforced. Product review
and confirmation actor triggers validate each distinct merchant principal after
the complete statement; a mixed valid/invalid statement rolls back every row.

Hash joins are preferred only inside matching set transactions, with JIT disabled,
so inaccurate statistics on a new import cannot turn a set join into repeated
scans. There is no session-global planner change. Work and audit storage grow with
input size; constant elapsed time for an unbounded catalogue is not claimed.

Migration **78** introduces criterion/application fields and immutable candidate
and result evidence. It retains historical migration-77 jobs/pages, marks unfinished
old searches stale and associates new after-import cycles with native jobs.
Rollback restores the original actor triggers when no matching evidence exists;
it refuses to discard approved/applied evidence. No production migration ran.

## Design and verification

Applied `styk-ui-workflow`, official frontend-design, design-system, design-critique,
accessibility-review, ux-copy and design-handoff. The existing STYK shell, font,
primary color and form patterns are reused. One white panel owns criteria, counts,
examples and actions; `products-automatching.css` owns its layout and loads last.
Long IDs are single-line ellipses with full-value titles. Primary actions precede
examples and remain in normal document flow; no fixed footer overlays rows.

Inspected `/products/matching` at 1440×1000, 1366×768 and 390×844: actual global
search/application, radio arrow-key selection, mobile picker/open options,
loading/disabled/success/zero states, long identifiers, exceptions and expanded
future automation. Mobile matching selects use 16px text and 44px height. Checked
computed padding, control geometry and overflow. Screen-reader behavior and a
complete WCAG audit are not established by these checks.

No local unit, integration or browser test suites ran. Local checks are limited
to check-plan, TypeScript, ESLint, build, serial Go build/vet and diff checks.
Remote contracts cover 6005-product atomic confirmation, exact concurrent retry,
source changes, injected persistence failure/rollback/recovery, canonical hashes,
all three criterion classifications and mixed-principal rejection.

[Remote verification on 165fc3c](https://github.com/lilter96/styk/actions/runs/42385745827)
passed frontend and PostgreSQL contracts. Its dedicated race-enabled performance
check on 2104 products measured:

| Criterion | Search | Apply through commit |
| --- | ---: | ---: |
| Article or code | 84 ms | 689 ms |
| Article | 71 ms | 671 ms |
| Code | 72 ms | 675 ms |

The dedicated CI check enforces <1 second for both operations for each criterion.
These are service/database measurements on the same CI host. The visible localhost
browser against a remote Neon branch measured 1189 ms HTTP / 1060 ms matching
service / approximately 1289 ms from automated click to the confirmed UI for 2104
pairs. Thus a universal sub-second click-to-result guarantee over that remote
connection has **not** been demonstrated. External providers are not called by
matching. The cloned synthetic environment performed no provider create/update.

[Full regression](https://github.com/lilter96/styk/actions/runs/42385748311) exceeded
its shared five-minute budget. Unfinished business/browser shards did not pass;
this is not full-regression acceptance. Final follow-up evidence is in Actions.

Browser screenshots: [automatic-matching evidence](evidence/automatic-matching/).
