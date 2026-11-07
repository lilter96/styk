# Products review follow-up, 2026-11-07

Base: master a9d5889 (includes the reviewed 340c6f8).

## Implemented correctness changes

- Auto-matching preview no longer saves policy. Modified policy requires a
  separate explicit Save rules action; its after-import consequences are stated.
- Auto-matching is keyed by tenant/account pair. Products workflows are keyed by
  pair and route, resetting import selections, edits and previews. Existing
  matching/import cleanup aborts obsolete requests. Submitted server operations
  are not cancelled or represented as rolled back by leaving the screen.
- CatalogSync already had account-keyed state. Its selection-saving preview now
  explicitly says Save selection and check, including the effect on automatic
  transmission for changed selections. This is the explicit-save fallback,
  not a new side-effect-free draft preview API.
- Creation form no longer claims to persist a draft. Return actions and text
  explain that edits last only until leaving the page. Durable server drafts
  remain unimplemented.
- Mobile account labels remain visible and wrap within the existing shared theme.
- Empty auto-matching groups explain the filter and how to return to all results.

## Evidence and checks

Used styk-ui-workflow with frontend-design, design-system, design-critique,
accessibility-review and ux-copy. Reused the current product patterns; research
and handoff deliverables were unnecessary for this corrective scope.

Browser: real app at localhost:5173/products/matching, 1440x1000, 768x900 and
390x844. Checked changed-policy notice, disabled preview, explicit save action,
account switching and restoration of original saved settings. Neither settings
save nor confirmation was submitted to the shared dataset. Mobile account pair
had no horizontal overflow; checkbox labels and keyboard-reachable actions were
inspected. This does not establish full accessibility compliance.

TypeScript, ESLint and Vite build were selected instead of test suites under the
user's local-test restriction. Regression coverage now asserts explicit saving
before preview and discarded drafts on account switch. CatalogSync tests use the
new action name. No claim is made that the seven externally reported failures
have been reproduced or resolved. Creation-editor/transfer visual acceptance is
still outstanding; only the matching screen and account labels were inspected.

## Remaining review scope

- Background/resumable matching for large catalogues; existing 2000/5000 guards
  must not simply be removed because candidate uniqueness depends on complete data.
- Multi-batch import beyond the current 20-item operation cap, preserving exact
  per-batch confirmations, retry identity and partial/unknown outcomes.
- Durable account/product/version-scoped server drafts and recovery across visits.
- Explicit counts/actions for linked vs unlinked bulk selections; richer name
  evidence and distinct limit/stale/transient errors.
- Resolve the seven reported test failures against real failure output, then
  CI acceptance. No local suite was started.
- CSS ownership cleanup, mobile typography, route bundle splitting and complete
  visual acceptance for all changed forms at 1440/768/390.
