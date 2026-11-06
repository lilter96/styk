# Verification by impact

`make check-plan` explains the checks selected from the current diff. `make check`
runs the fast checks in that plan. Neither command starts the full integration
suite. Committing, pushing or asking for status does not require rerunning a
previously passed check when its inputs have not changed.

The implementation is shared by local development and CI:
`cmd/checks` and `internal/checkpolicy`. Do not substitute an agent's guessed list
of tests for this plan, or use the old blanket “run everything” instruction.

| Change | Fast checks | Additional selected coverage |
| --- | --- | --- |
| Documentation | Diff whitespace | No application tests or PostgreSQL |
| Frontend | TypeScript, ESLint, frontend unit suite, production build | Isolated presentation specs for changed UI areas |
| Go source | Formatting, golangci-lint, unit tests with `-race`, build | Changed packages and transitive consumers, including test imports |
| Go unit test only | Declaring package | Consumers are not invalidated by test-only edits |
| Posting query/filter SQL | Affected Go checks | Search/status, cursor pagination and tenant/account isolation contracts |
| Orders/catalog/stock behavior | Affected Go checks | Reviewed component contracts and matching PostgreSQL test companions |
| Integration test only | Declaring package | Tests discovered from that file's Go syntax |
| Schema, Go dependencies, shared security/dispatch, process harness or unknown impact | Fast checks are preliminary | Full regression required in CI |
| Verification policy/workflows | Planner/runner tests and workflow lint | No application database or full business regression |

Integration selection is a reviewed contract set, not a claim that every
behavioral dependency can be inferred from filenames. Unknown boundaries and
shared/process test helpers expand coverage. Nightly full regression covers the
remaining combinations. Add a rule and classification tests when introducing a
new component; do not silently drop an unknown path.

## Commands

```sh
make check-plan
make check
make check-ui                 # when browser specs are selected
make check-integration        # when component contracts are selected
make check-plan BASE=origin/master HEAD=HEAD
```

Without refs, the diff includes staged, unstaged and untracked files. Committed
comparisons use the merge base and include both sides of a rename/deletion;
unrelated working-tree files do not enter a CI comparison. Plans list package
names, browser specs, integration test names and the reason for each rule.

Run each selected lane once. Go's ordinary test cache is retained for unit tests;
database contracts use `-count=1` because the database is mutable. Race-enabled
tests already include the ordinary test execution. Do not follow them with the
same suite without `-race`, or rerun completed lanes after a docs-only follow-up.
Change, failure or unresolved risk in a lane justifies rerunning that lane.

`make check-integration` uses a supplied `TEST_DATABASE_URL` only after checking
the effective pgx host, fallback hosts and database against the two dedicated
loopback test databases. Application databases and query overrides are rejected.
Otherwise it starts PostgreSQL 17.6 in an owned disposable Docker container on a
random loopback port, prepares the companion `styk_orders_point_test` database
for native-point contracts, applies migrations there and removes it after success,
failure or cancellation. It never migrates the application's `.env` database.

Install frontend dependencies with `npm ci` in `web` and Chromium with
`npx playwright install chromium` before local browser checks. The presentation
runner owns a random loopback HTTP port and has no provider/application API
proxy. It preserves other running previews. POSIX cancellation terminates owned
process groups, including worker/test grandchildren. Process-backed browser
acceptance retains the existing POSIX requirement.

## CI and full regression

Pull requests and pushes to `master` use the same planner. Go, frontend and
component integration jobs run independently when selected; frontend/docs changes
do not provision PostgreSQL. Feature-branch pushes are covered by their PR rather
than a second identical push workflow. Superseded branch runs are cancelled.

The stable `test` status requires the plan and every selected job to succeed.
An unselected job may be skipped; a selected-but-skipped, failed or cancelled job
cannot satisfy the gate. Workflow syntax and gate behavior have their own checks.

Full regression runs at **02:15 UTC daily**, by explicit Actions dispatch, or for
high-risk plans. `make check-full` is the explicit local equivalent. It runs the
Go integration build once with `-race` (including ordinary Go tests), rather than
separate ordinary/race copies; frontend checks and browser acceptance also run
once. The full lane includes the existing independent order process/browser
flags and tests under `internal/` that the previous workflow omitted.

Fast Go tests have a two-minute per-package timeout; selected integration groups
have an eight-minute timeout. The full Go suite retains its forty-minute bound.
These are failure bounds, not promised run times or instructions to run a full
suite on every task. A routine push must not wait for a newly invented full run;
high-risk merges must satisfy their selected CI gate or an explicit owner waiver.

All automated entry points scrub provider credentials, live opt-ins and PostgreSQL
environment defaults. They do not read `.env` or local credential files. Legacy
`make lint`, `make test`, `make build` and `make test-integration` remain available
for explicit broad diagnostics; they are not the default completion checklist.
Operational `make up`, `make down`, `make migrate` retain their `.env` behavior.

Report the actual result of each required lane. A stopped, omitted or still-running
suite is not a passing suite. Normal tests must never use real provider services.
