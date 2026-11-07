# Lifecycle verification ledger — 2026-11-07

Base: `origin/master@340c6f884046e8119de6b328019ed4c72b1cb303`.
This file begins with the implementation-phase automated verification. The
subsequent real-provider run is recorded separately in
[the LIVE acceptance report](live-orders-acceptance-2026-11-07/executive-summary.md).
All provider mutations in the implementation-phase results below target isolated loopback HTTP contract fixtures.
PostgreSQL is dedicated disposable PostgreSQL 17.6. No shared development or
production migrations, provider writes, push, merge or deployment were performed.

## Final result

**`make check-full`: PASS, 255.627 s (4m15.627s), exit 0**, including owned
database creation, migrations, every selected execution group, build and cleanup.
The hard total budget is five minutes; dependency installation is machine setup.
No test-only filter omitted coverage. The full package list and the compiled
business list were partitioned exhaustively and checked for exactly one terminal
outcome per name.

- Go race/integration: **1,608 top-level PASS, 27 top-level SKIP, zero FAIL**;
  including subtests, **4,311 PASS events / 29 SKIP events**.
- Business integration specifically: **580/580 names accounted for exactly once:
  564 PASS / 16 SKIP / zero FAIL**. Independent ordinary/native nine-process
  stacks and both enabled real merchant browser journeys executed.
- Frontend: **403 tests / 47 files PASS**; TypeScript, ESLint and production
  build PASS. The existing large-chunk advisory remains an advisory.
- Presentation browser: **60/60 PASS**, including financial/lifecycle states at
  1440/1366/390 CSS px; Go/workflow lint and normal application build PASS.
- Fresh migrations **1–73**, guarded/clean rollback and upgrade fixtures ran on
  disposable PostgreSQL. All owned test processes/containers were removed.

The skips are explicit: five direct subprocess helpers (their parent scenarios
execute them), two live-provider opt-ins, nine contracts requiring separately
named credential/connection databases, and thirteen optional browser cases (eleven
top-level plus two Ozon/MoySklad import-management subcases). No new flag disabled
these groups. Their omitted configurations and live checks are not reported as PASS.

Evidence: `.local/orders-lifecycle/check-full-five-minute-attempt7.log`,
`check-full-five-minute-attempt7-result.json`, and its source manifest SHA256
`6581e577a668eaf7864ec5591ce1a9767c362a22febfa5564e9bf3cfebde5838`.
Exhaustive business assignments and separate JSON logs:
`.local/checks/full-2514341825/coverage.json` and `worker-1.jsonl` through
`worker-8.jsonl`. Earlier failed/stopped attempts below are retained as such.
The compact aggregate with every skip name/reason is
`.local/orders-lifecycle/acceptance-five-minute.json`.

After this application proof, only `regression.yml` changed: CI now pre-pulls
PostgreSQL during setup and uses the exact owned-container path and limits tested
locally. The actual verification step retains its five-minute limit. Actionlint
and race-enabled runner/gate unit checks passed (1.277 s / 1.023 s); source
comparison confirmed application, test and Go inputs were unchanged. The full
application suite was correctly reused, not redundantly repeated for that YAML
and documentation follow-up. GitHub-hosted execution itself was not performed.

## Executed lanes

| Command/scope | Result | Evidence/limits |
| --- | --- | --- |
| `make check-plan` | PASS | Schema/shared dispatch classified high risk; full lane required. `.local/orders-lifecycle/check-plan.txt` |
| Initial `make check` | FAIL, corrected | Backend lint/race/build passed; first frontend run overlapped unfinished UI. Later run exposed seven pre-existing Products test failures. Neither failed command is relabeled PASS. |
| Clean baseline Products tests | 7 FAIL / 56 PASS | Reproduced on archived clean `origin/master@340c6f8`, `.local/lifecycle-baseline/frontend-tests.log`; five test files subsequently repaired without Product runtime edits. |
| `go run ./cmd/checks run --lane go` | PASS | Selected lint, race unit tests and build. `.local/orders-lifecycle/check-go-final.log`; later narrow SQL/packing corrections have named supplemental coverage below. |
| `npm --prefix web run test` | PASS: 392 tests / 46 files | `.local/lifecycle-frontend-final.log`. No tests removed/skipped to obtain this result. |
| Frontend typecheck, lint, build | PASS | Full checks plus later affected-file checks. Existing large-bundle advisory is not a build failure. |
| Selected presentation browser lane | PASS: 59 distinct scenarios | 58 unchanged scenarios passed selected run; final lifecycle scenario passed separately after correcting its assertions/layout. No unchanged browser checks were relabeled a newly repeated full run. |
| Final lifecycle frontend scope | PASS: 10 tests and browser scenario | Exact opt-in/save/revoke, ownership copy, blockers, report precision/pagination, keyboard and responsive layout. |
| Initial `make check-full` admission | FAIL before tests, corrected | A concurrently edited new test file needed gofmt; no application suite started in that invocation. |
| Earlier `make check-full` | FAIL: cumulative 40-minute test timeout | `.local/orders-lifecycle/check-full.log`. All migrations and lint passed, as did internal/cmd race packages. The business package exhausted its cumulative deadline. Three earlier failures were the startup gate and two subsequently corrected reserve fixtures. This run is not relabeled PASS. |

## Five-minute verification budget

The owner subsequently required **at most five minutes for the whole verification
run**. The previous 40-minute timeout and a proposed sequence of test partitions
are superseded. No partitioned replacement was run or claimed as coverage.

- Actual repeated-computation bottleneck: each provider-client construction
  decoded and fully validated the embedded 808 KiB contract manifest. Under
  `-race`, `BenchmarkContractManifest` measured **115,107,047 ns/op**, 5,381,020
  bytes and 11,609 allocations. The validated embedded snapshot is now cached
  once, with independent deep copies of every mutable nested slice; dynamic
  `DecodeManifest` still performs strict validation. The same benchmark measured
  **391,882 ns/op**, 279,776 bytes and 2,306 allocations: approximately **294×**
  faster. Mutation isolation and strict/tampered-manifest tests remain enabled.
- Database fixtures previously started `go run goose up` and reapplied all
  migrations for every isolated schema. They now migrate one **empty** template
  and clone a distinct database per fixture when the dedicated role has CREATEDB;
  restricted roles retain the isolated-schema fallback. Queues, sequences,
  mutable rows and leases are not shared. Two new isolation/name-safety tests
  passed with race detection in **3.347 s**; six financial PostgreSQL safety and
  migration cases passed in **4.386 s**.
- Legacy rollback tests also invoked `go run goose down` once per migration:
  forty-one tool launches made one case take 46.15 s. All integration migration
  commands now reuse the exact pinned Goose v3.25.0 binary built once into an
  owned temporary GOBIN; every migration step and rollback assertion remains.
  The complex historical-refusal case passed in 4.20 s and independent fixture
  isolation in 2.22 s (combined race run 7.571 s, including tool setup/cleanup).
- The automatic multi-target stock scenario contained a real thirty-second wait.
  `stocksync.Runner` now accepts an optional workflow clock, defaulting to real
  UTC. The scenario explicitly asserts an immediate repeat is rejected, advances
  the clock thirty-one seconds and asserts the next write/readback. Production
  one-second account and thirty-second target SQL limits are unchanged. The
  complete multi-scope/UNKNOWN/zero-transition case passed with race detection in
  **18.836 s** under a thirty-second deadline.
- A real PostgreSQL 16 compatibility error found by the HTTP fixture was fixed:
  provider admission clamps initial `-infinity` timestamps **before** subtraction.
  Existing future cooldown and inflight checks are preserved.
- A second repeated CPU bottleneck was `identity.New`: it regenerated the
  fixed dummy bcrypt hash at cost 12 for every store. A financial fixture created
  four stores, spending about eleven seconds before its first provider read.
  That immutable dummy hash is now generated once per process and defensively
  copied. One race benchmark measured repeated construction **2,200,364,616
  ns/op → 3,685 ns/op**. Real password hashing retains cost 12, fresh salts,
  unchanged password comparison and four per-store hash slots. Concurrent copy
  isolation/unit/benchmark passed in 5.402 s; the actual PostgreSQL regression
  proving the known dummy plaintext cannot log in a missing user passed in
  10.310 s. `.local/orders-lifecycle/identity-cache-final.log`.
- The shared runner allows **4m40s work + 20s owned cleanup**. Lint, full Go
  race/integration/build and frontend/browser run as three independent lanes.
  The complete Go suite still runs once with `-race -tags=integration -count=1
  ./...`; database packages remain serial. No test filters or heavy-group skips
  were introduced. Cancellation terminates owned process groups, including
  descendants that ignore TERM; the browser runner also bounds its own group.

The final measured five-minute invocation is recorded above; the configured
deadline alone did not establish passing tests.
An intermediate bounded attempt was stopped at **75.849 s** when the second
bottleneck was identified. Its lint also exposed a subsequently fixed self
assignment in source diff normalization. That partial run remains
`STOPPED_BY_REQUEST`, never PASS; its log/result are retained separately.
The next attempt correctly stopped on its global work deadline at **280.712 s**.
Frontend 399 tests, all 60 presentation cases, lint and internal Go packages had
passed; business integration was unfinished. This exposed the repeated Goose
launches and real stock cooldown wait above. It remains
`INCOMPLETE_WORK_DEADLINE`, not PASS. The financial retry identity guard was
strengthened before the subsequent final run; its new PostgreSQL cases passed
in 4.515 s and four matching frontend negative cases were added.

The serial business package still exceeded the global budget in attempt 3
(280.529 s). Full execution therefore now enumerates **all** Go packages and the
compiled business binary's complete Test/Example/Fuzz list. Each business entry
runs exactly once across eight concurrent processes under the **same** global
work deadline. Every process has an independently migrated database on one owned
PostgreSQL server; schema isolation alone did not isolate database-wide advisory
locks. Other packages run with `-p=4` after auditing their fixture isolation.
Per-worker JSON logs and an exhaustive assignment manifest are retained and
verified: missing, duplicate or unexpected top-level terminal outcomes fail the
run. Top-level patterns include every subtest/fuzz seed; benchmarks keep their
ordinary explicit opt-in behavior. This is parallel execution within one budget,
not consecutive five-minute subsets.

Attempt 4 failed in 254.859 s: shared-schema advisory contention, exhausted DDL
lock capacity, and one asynchronous UI assertion. Separate worker databases,
bounded test-server capacity (200 connections/512 locks), and awaiting the actual
rendered UI fixed those issues. Attempt 5 failed in 246.484 s: it exposed two
stale previously unobserved tests and uneven work allocation. The migration test
now targets migration 69's Down guard in a rolled-back transaction and compares
the retained audit/schema snapshots. The stock test now uses the actual endpoint
policy and verifies all nine operator facts remain attested rather than provider
verified, with unattested calculations blocked. Production behavior was unchanged.

Attempt 6 failed in 247.398 s: two processes hit an earlier four-minute local
timeout while the global deadline still had time, and the real-process browser
expected seven navigation links while the current main branch has six. Children
now inherit the remaining absolute global work budget. The browser asserts the
exact six approved labels and hrefs. A measured-cost assignment balances the
other seven workers; the two cases sharing a Vite port are kept together and
their output directories are private. No test was removed or waived to repair
these attempts. The final full run below is the completion authority.

## New real-adapter database/HTTP acceptance

The following commands use `-race -tags=integration -count=1`, a dedicated
`TEST_DATABASE_URL` and no live-provider opt-ins. Test connection strings are not
part of the report. The standard full lane provisions its own database.

```sh
go test -race -tags=integration ./tests/integration \
  -run '^TestOrdersMerchantAssemblyHTTP' -count=1 -timeout=6m

go test -race -tags=integration ./internal/postgres \
  -run '^TestMerchantIntentPostgres' -count=1 -timeout=5m
```

- Assembly: **5 top-level tests PASS**, 127.241 s, including prerequisite and
  final-admission subcases. The real merchant API records owner consent, the
  actual leased ingestor journals an MS transition, the existing outbox/worker
  sends one v4 ship through coordinated adapters, ACK waits for actual get
  readback, replay makes no second write and no stock document is implied.
  Unknown requirements, mandatory marking without evidence, explicit action
  denial, changed native composition, competing forward refresh and consent or
  native-state changes after provider admission all prevent the unsafe write.
- Native lifecycle: **6 top-level tests PASS**, 128.708 s, including three
  missing/null/foreign-demand subcases. Physical handoff gives one applied demand
  and stock `10 → 9`; genuine repeated transition cannot reopen gross allowance;
  an exact physical return links to its applied demand and restores `10`.
  UNKNOWN/restart reads instead of resending; two schedulers bind one work;
  revocation blocks unsent work; overlapping updated polling deduplicates.
- Native reserve: **2 additional top-level tests PASS**, 36.845 s. A real owned
  mirror reserve patch/reconciliation establishes `10 / 1 / 9`; an applied demand
  gives `9 / 0 / 9`. A provider that retains the reserve stays `POLLING` with
  `NATIVE_PARENT_FACTS_NOT_CONFIRMED`, without a forced clear or second write.
- Merchant journal PostgreSQL: **7 top-level tests PASS**, 9.538 s. Scoped
  configuration/work foreign keys, source/action dedupe across revisions,
  immutable payload/delete/terminal result, concurrent CAS/loser rollback,
  historical rollback refusal and clean empty down/up are checked. The paired
  rFBS handoff test proves carrier-before-native admission, target fencing and
  isolation of unrelated scopes/source revisions.
- Native state provenance PostgreSQL: named integration test PASS. Exact
  reconciled state-writing evidence is reconstructed without acquiring foreign
  scope authority.
- Final narrow Go supplements PASS: merchant transient-read classification,
  MS-primary state ownership, pending-intent metadata fencing, policy consent,
  timestamps/scheme/replay, updated-window bounds/ties/foreign account, native
  linked-demand parsing and exact automatic reserve release. Whole affected
  `orderflow` unit package was rerun after the final dispatcher extraction.

The assembly fixture starts with a reconciled native customerorder. Initial source
discovery/customerorder creation is covered by existing retained contracts, not
claimed as part of one new uninterrupted monthly end-to-end scenario. Native
monthly stages are tested in separate retained scenarios. There is no claim of
one new uninterrupted source-to-monthly scenario or live business transaction.

## Financial draft acceptance

`go test -race -tags=integration ./tests/integration -run
'^TestOrdersCommission(HTTP|Schema)' -count=1 -timeout=60s`:
**four real-HTTP journeys plus the schema-73 gate PASS, 46.400 s**.
The real merchant API reads native metadata before enrollment, records separate
finance consent, creates an exact-decimal preview, idempotently approves it and
runs the shared PostgreSQL outbox through a real one-POST native adapter. ACK
requires independent header/positions readback before the journal reports the
draft confirmed. Lost ACK and runner restart do not resend and retain the original
UNKNOWN receipt. A changed authoritative source records a correction and blocks
the unsent draft; consent revoked after actual provider admission prevents POST.
The startup gate rejects an old version, absent ledger or missing scoped FK.
Evidence: `.local/orders-lifecycle/commission-http-final.log`.

Native finance adapter and domain race tests cover negative commission, wrong
contract/currency, preexisting or duplicate markers, edited/applied/deleted
documents, exact ACK document identity, unknown outcome, revoked admission,
price/VAT/return/component restrictions and versioned source-row diffs.
Six PostgreSQL safety cases additionally cover stale claim tokens, foreign scope,
zero-dispatch false reconciliation, immutable plan/terminal evidence, approval
rollback, period dedupe and clean/refused historical migration rollback.

## Browser evidence

Applied skills: `styk-ui-workflow`, official `frontend-design`, `design-system`,
`design-critique`, `accessibility-review`, `ux-copy`. Research/synthesis/handoff
skills were unnecessary for this bounded extension of an existing product.

Routes/states: `/orders/settings?section=lifecycle`; disabled/enabled policy,
explicit consent, save/revoke, expanded predecessor selection, long native state
names, paired rFBS actions, incompatible acceptance option, journal blockers,
closed-period report loading/error/rows/pagination and exact large decimals.

Viewports: **1440, 1366 and 390 CSS px**. Checked keyboard focus/operation,
44px controls, measured 3px focus outline, new action text contrast ≥4.5 in normal
and hover states, no horizontal overflow and no save-footer/list overlap.
The final mobile result was inspected after the ownership/blocker-copy follow-up.
This is scoped evidence, not a full WCAG or screen-reader certification.

Financial UI adds actual contract selection, explicit price/rate/VAT and separate
consent, draft preview/review, source correction row diff, known-unsent retry,
durable UNKNOWN after reload and independently confirmed native document links.
Its final focused scope has **17 tests PASS** and **two browser cases PASS** at
1440/1366/390 px; TypeScript/ESLint/build passed. Full frontend and presentation
coverage is repeated by the final bounded full lane.

Artifacts: `.local/orders-lifecycle/finance-focused/` (latest finance and lifecycle
screens), and `.local/orders-lifecycle/focused/` including
`lifecycle-computed-layout.json`. Root additionally inspected the rendered mobile
financial preview. This remains scoped visual/a11y evidence.

## Not established

- Active production Ozon assembly, integrated/non-integrated/Express delivery,
  warehouse handoff, acceptance and provider stock effects: **NOT_OBSERVED_LIVE**.
- Production MS response coverage of empty `customerorder.demands`; omitted/null
  coverage intentionally blocks automatic demand.
- Atomic provider CAS or a transaction spanning Ozon and MoySklad; a concurrent
  external native document created after the final GET remains a provider race.
- Native monthly **draft** creation and scheduling are implemented and tested;
  production financial outcomes remain **NOT_OBSERVED_LIVE**. Posting/applying
  reports, nonzero report returns/services/unsupported loyalty, VAT-exclusive
  mapping, per-posting/async report reads and native correction writes are not
  implemented. Corrected sources are retained and compared for manual review.
- Carriage batch creation/approval remains **not implemented**. Current discovery
  schema/canonical approval verification was unavailable in this session; this
  is not a claim that the provider forbids a batch writer.
- Automatic partial movements, unknown/hybrid delivery authority, acceptance
  evidence and missing mandatory packing/marking inputs remain reviewed/blocked.

See [implementation and first-seller runbook](orders-fbs-rfbs-lifecycle.md) and
the linked versioned primary-source contract notes for the precise boundaries.

## Final post-LIVE regression — 2026-11-07

After the three observed provider compatibility fixes and their regression tests,
one full run passed in **259.329 s**, exit 0, unchanged source manifest
`ffe891fbadda461d5db06a7165ae90b85cf0926930aff69963868abf549e9bcb`.
Go: 1628 top-level PASS / 27 SKIP, including subtests 4430 PASS / 29 SKIP,
zero failures. Business: 582/582 names accounted exactly once (566 PASS,
16 SKIP). Frontend 403/47 files and presentation browser 60/60 passed; lint,
typecheck and builds passed. Owned disposable PostgreSQL/process cleanup passed.
See [the exact final verification ledger](live-orders-acceptance-2026-11-07/verification.md)
for command artifacts, supplementary failed checks, skips and runtime evidence.
Physical production fulfillment and real native accounting remain separately
qualified in the [LIVE scenario matrix](live-orders-acceptance-2026-11-07/scenario-matrix.md).
