# Products review completion, 2026-11-07

Base: master `3ac695e`, including reviewed Products `340c6f8` and the subsequent
orders/schema work. This iteration keeps the approved STYK visual system.

## Completed behavior

- Matching policy changes require **Save rules**. Starting a check does not save
  an edited policy or enable after-import confirmation. Account/route changes
  reset local selections and abort obsolete requests.
- Large catalogues use durable background jobs, pinned to both complete import
  snapshots and the saved policy revision. Each worker transaction commits at
  most 100 source products, its preview, cursor, counts and page pointer together.
  Process interruption resumes from committed progress. A transient failed job
  has an explicit Continue action; changed imports, policy or authorization require
  a fresh check. Completed results, the selected page and uncertain confirmation
  keys survive reload. Page transitions clear selection/acknowledgement and block
  confirmation until the requested page finishes loading.
- Exact candidate uniqueness and source-offer duplicates are checked against the
  complete indexed snapshots, including products outside the current page.
  Names remain review hints: at most 100 shared-token candidates plus exact-name
  candidates are ranked, with three suggestions displayed. Missing hints are not
  evidence of absence; manual search remains available. The UI shows similarity
  and shared words. Names never authorize automatic confirmation.
- Standing after-import rules also traverse the entire catalogue with a persisted
  cursor. Shared worker turns alternate matching with existing publication work.
  Read-only job processing remains available when catalogue publication is off.
- Imports retain selection across catalogue pages. A large selection is checked
  and approved sequentially in batches of at most 20; that is an operation size,
  not a limit on the whole selection. Review and confirmation paginate the entire
  selection. Each actual server preview ID and its selected enrollment IDs remain
  separate; the combined display is never submitted as a synthetic server plan.
- The exact approval manifest is saved before any approval, then each attempted
  request and receipt are saved before progressing. Recovery repeats original
  keys/payloads and skips recorded operations. Unknown outcomes stop the sequence;
  definite first-attempt rejections retain their products for correction. A queued
  operation is never presented as a created provider product.
- Drafts persist on the server per tenant/account pair and import direction.
  They retain product identities/source revisions, edited fields, category facts,
  checked plans and approval manifests. Optimistic revisions prevent another tab
  from silently replacing a draft; exact save retries are idempotent. Both creation
  forms expose a real Save draft action. Drafts are editable input, never dispatch
  authority: canonical preview/approval checks still run before provider work.
- Bulk selection reports linked/unlinked counts and names the actual subset used
  by matching versus transfer. Mobile account names remain visible.

## API and storage contract

- `POST /api/product-matching/jobs`: scope, expected policy revision and request
  key; replay returns the existing job. `GET /api/product-matching/jobs/{id}?page=N`
  returns durable progress/global counts and a completed result page (zero-based).
  POST `/{id}/retry` resumes transient failure using the original pinned inputs;
  POST `/{id}/cancel` stops further processing without deleting audit history.
- `POST /api/product-matching/preview` accepts `after` for an explicit recheck of
  the current page. Confirmation revalidates that page and the original snapshots.
- GET/POST `/api/product-import-draft`: scoped provider/direction, revision, state,
  and save key. A stale writer or changed same-key request receives 409. Client
  drafts are bounded to 7 MiB, database states to 8 MiB; oversize errors occur
  before admitting approval work. Source identities/scopes are checked on save.
- Work creation uses existing authenticated mutation/CSRF and connection access
  controls. Jobs are scoped to their initiating actor. Saving artifacts may
  initialize an absent disabled catalogue configuration; it does not activate
  synchronization, automatic matching, or replace existing policy.
- Migration **77** adds draft/job/page storage, matching indexes/functions and the
  standing-rule cursor. API/worker schema readiness requires it. Deploy migration
  before starting the new API/worker. Down refuses when drafts/jobs exist, retaining
  user work; use a forward correction then. No production migration was performed.

## Styling and interaction ownership

Applied `styk-ui-workflow`, official `frontend-design`, `design-system`,
`design-critique`, `accessibility-review`, `ux-copy` and the existing handoff rules.
No new research was needed for the supplied review and known user corrections.

`main.tsx` explicitly loads matching/catalog foundations before Products styles.
Lazy routes do not import competing CSS. This fixes direct-load/navigation cascade
changes, compact-table overflow and button contrast found by browser CI. Removed
24 identical shadowed declarations. Affected small text uses 12–13 px rather than
10–11 px; mobile creation inputs use 16 px and 44 px controls. A bounded matching
list scrolls independently; actions/pagination stay in normal flow. The initial JS
chunk is about 475 kB and Products is a separate approximately 203 kB route chunk.
The shared CSS remains approximately 294 kB; this is not a full CSS rewrite.

See [frontend styling guidance](frontend-styling.md) for the explicit CSS-order rule.

## Verification evidence

No local unit, browser or integration test suite was run, as requested. Local
`make check-plan`, TypeScript, ESLint, production build, serial Go API/worker build,
scoped Go vet and diff whitespace checks were used; tests execute on CI runners.

Manual browser/API verification used a separate expiring Neon copy and visible
localhost preview, without a general publication worker. A real background job
processed **2105 products in 22 pages**; a duplicate target outside the source page
remained a conflict. Reload restored the result, and page 22 contained five products
with Next disabled. A **40-product draft** and edited name/description survived
reload. Both import directions exposed real saves; Ozon commercial fields and
category loading were inspected. No provider create/update operation was submitted.

Creation form inspected at 1440x1000 and 390x844; import list at 768x900; matching
result at 1440x1000 and 390x844. Checked long text, loading/disabled states, selection,
first/last result pages, computed scroll/control geometry, Escape and focus return.
Mobile draft input measured 16 px/44 px with no page overflow. Screenshots:
[evidence](evidence/products-completion/). These checks do not establish a complete
screen-reader audit or successful provider creation.

Remote evidence before final master integration:
- [Frontend 423 passed / 0 failed, plus 63 browser scenarios](https://github.com/lilter96/styk/actions/runs/42379511480).
- [Race-enabled matching/postgres/console/catalogsync checks and selected PostgreSQL contracts](https://github.com/lilter96/styk/actions/runs/42379143292).
- [Full regression attempt](https://github.com/lilter96/styk/actions/runs/42379145832) did not complete within the shared five-minute budget. Interrupted business shards did not pass. Its frontend phase exposed debounced-result test races; the test now waits for the current response rather than a replaced DOM node. Final follow-up results are recorded in GitHub Actions.

The seven failures reported for the older 387-test review were not reproduced on
the starting master: remote baseline was 420/420. Current coverage adds multi-batch
selection, durable draft restoration, exact-key recovery after reload, global
cross-page duplicates, resumable progress and rollback protection. No claim is
made that an interrupted full regression or provider writes passed.
