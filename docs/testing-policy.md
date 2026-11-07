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
After a command exits, the runner also removes remaining members of its owned
process group. This covers a test binary's own timeout or early exit, when
context cancellation would otherwise never run and helper workers could survive.

## CI and full regression

Pull requests and pushes to `master` use the same planner. Go, frontend and
component integration jobs run independently when selected; frontend/docs changes
do not provision PostgreSQL. Feature-branch pushes are covered by their PR rather
than a second identical push workflow. Superseded branch runs are cancelled.
The frontend job runs its selected unit/build and browser checks together through
the `presentation` lane under one five-minute verification step. Go and selected
integration verification steps have the same limit; dependency installation is
separate setup, with no tests moved into those setup steps.

The stable `test` status requires the plan and every selected job to succeed.
An unselected job may be skipped; a selected-but-skipped, failed or cancelled job
cannot satisfy the gate. Workflow syntax and gate behavior have their own checks.

Full regression runs at **02:15 UTC daily**, by explicit Actions dispatch, or for
high-risk plans. `make check-full` is the explicit local equivalent. It runs the
Go integration build once with `-race` (including ordinary Go tests), rather than
separate ordinary/race copies; frontend checks and browser acceptance also run
once. The full lane includes the existing independent order process/browser
flags and tests under `internal/` that the previous workflow omitted.
The full CI job pre-pulls the PostgreSQL image during setup and lets the runner
own the same disposable PostgreSQL configuration used locally, including the
companion native-point database and cleanup; it does not substitute a service
container with different connection or DDL-lock limits.

Every verification run has a **five-minute total wall budget**, including planning,
database preparation and cancellation/cleanup. The runner stops work after four
minutes forty seconds, reserving twenty seconds for owned process groups and the
disposable PostgreSQL container. This is one deadline for the entire run, not a
fresh five minutes for each package, partition or retry. A deadline is a failed or
incomplete result, never a passing regression. Dependency/tool installation
(`npm ci`, downloading Chromium and the Go toolchain) is a separate machine setup
step; warm dependencies do not justify dropping any test from verification.

Full regression runs three independent lanes concurrently: Go/workflow lint;
the complete race-enabled Go integration build followed by the Go build; and
frontend type/lint/unit/build followed by browser acceptance. Go packages are
enumerated from `go list -tags=integration ./...` and divided exhaustively between
ordinary packages (`-count=1 -race -tags=integration -p=4`) and the business
integration package, compiled once with those same race/integration settings.
The compiled binary's complete Test/Example/Fuzz list is assigned exactly once to
eight concurrent processes. Anchored top-level patterns execute every subtest and
fuzz seed; benchmarks retain their ordinary opt-in behavior. Each process has its
own fully migrated root database on the one owned server, its own environment, and
at most two parallel test cases. Existing per-test database clones remain isolated.
The two real-browser cases that own the same Vite port have one dedicated
sequential worker. Other cases are assigned greedily by recorded top-level wall
times, so costly workflows do not collect in a worker merely because a new test
shifted a list index. The checked-in cost table affects scheduling only; every
name still runs once. Other package fixtures use their existing independent
databases/schemas.
Separate databases also isolate database-wide PostgreSQL advisory locks used by
legacy stock schedulers. Internally created worker names have a strict owned
prefix; the public supplied-DSN allowlist still accepts only the dedicated base
databases. Owned Docker PostgreSQL allows at most 200 connections and reserves
512 locks per transaction for concurrent migration/fixture cleanup. These settings
apply only to the disposable test server, not a supplied database or production.

Each worker writes a separate JSON log under `.local/checks/full-*`; the runner
checks that every enumerated top-level name has exactly one terminal outcome and
rejects missing, duplicate or foreign outcomes. The manifest records the complete
disjoint assignment. A failed worker or incomplete coverage fails the full lane.
There are no omitted groups, `-short`, retries, repeated ordinary/race suites or
consecutive partition runs with renewed deadlines. All eight workers share the
same earlier global deadline; increasing concurrency never increases its budget.
Fast Go tests retain a two-minute per-package timeout; integration and broad Go
tests have a four-minute per-package timeout inside the earlier global deadline.
Business subprocesses derive their test timeout from the remaining shared work
deadline, so preparation time or a worker start never renews the global budget.
Independent lanes retain all failures; cancellation terminates their owned
process groups. PostgreSQL fixtures may reuse a migrated empty template only when
every test receives an independent database, preserving mutable rows, sequences,
leases, rollback, concurrency and migration assertions.

A routine push must not wait for a newly invented full run; high-risk merges must
satisfy their selected CI gate or an explicit owner waiver. Improve fixture setup
or repeated pure computation when a run exceeds the budget; do not raise the
deadline, hide slow tests or split a suite into consecutive five-minute runs.

All automated entry points scrub provider credentials, live opt-ins and PostgreSQL
environment defaults. They do not read `.env` or local credential files. Legacy
`make lint`, `make test`, `make build` and `make test-integration` remain available
for explicit broad diagnostics; they are not the default completion checklist.
`make test` runs all Go unit tests with race detection through the bounded runner.
`make test-integration` runs the complete Go integration build with race detection,
including the same eight exhaustive business workers and other Go packages,
without frontend checks. It owns a disposable database (or uses a validated
dedicated loopback database). Both scrub the same environment and obey the same total deadline;
neither implicitly opts into live provider tests.
`npm run test:browser` delegates the existing nine-process browser contract to
the same bounded runner, with owned disposable PostgreSQL by default. The
standalone presentation runner also gives Playwright one 280-second global
deadline and awaits its owned browser-group shutdown.
Operational `make up`, `make down`, `make migrate` retain their `.env` behavior.

Report the actual result of each required lane. A stopped, omitted or still-running
suite is not a passing suite. Normal tests must never use real provider services.
