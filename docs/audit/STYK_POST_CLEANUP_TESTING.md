# Testing report

All PASS entries below correspond to commands/actions actually executed in this task. Final runtime smoke was rerun after replacing the native API/worker binaries.

| Check | Final result | Command / action | Notes |
|---|---|---|---|
| Formatting / lint | PASS | make lint | scoped source search; zero golangci findings |
| Go vet | PASS | go vet ./... inside make lint | actual executed subcommand |
| Go unit tests | PASS | make test | complete default package set |
| Go race | PASS | go test -race ./... | complete default package set |
| Named binaries | PASS | make build | existing API/worker/CLI targets |
| All Go packages | PASS | go build ./... | includes dev seed command |
| DB migrations | PASS | make migrate on isolated DB | existing 44 migrations |
| DB regressions | PASS | make test-integration | full final run, approximately 234 seconds |
| Seed DB / race | PASS | go test -race -tags=integration ./internal/devseed -count=1 | separate empty marked PostgreSQL instance; rollback/concurrency/isolation |
| TypeScript | PASS | npm run typecheck | real console |
| Frontend ESLint | PASS | npm run lint | real console and test sources |
| Frontend unit tests | PASS | npm test | 203 tests, 18 files |
| Frontend build | PASS | npm run build | real console |
| Standalone demo build | PASS | npm --prefix ../styk-demo run build | existing warnings retained |
| Landing/nested demo build | PASS | npm --prefix ../styk-landing run build | prerender/copy-demo included |
| Auth smoke | PASS | python scripts/dev-demo.py check | login 204, session 200 |
| Browser pages / security | PASS | python scripts/dev-demo.py smoke | 2 tests; six pages; cookie/CSRF/foreign scope/anonymous/retired login |
| Database integrity | PASS | guarded FK validation + count/arithmetic/scope queries | zero FK validation failures, foreign-tenant rows and stock/price inconsistencies |
| Docker readiness | PASS | pg_isready; docker ps | PostgreSQL ready, Mailpit healthy |
| Worker readiness | PASS | owned process + outbox query | one worker, 12 mock tasks succeeded |
| Seed repeat | PASS | same seed/reference time twice | second result existing=true; counts unchanged |
| Reset/restore | PASS | APP_ENV=development demo-db.py reset --confirm styk_demo_dev | real backup/restore/count comparison; schema/Goose history retained |
| Production reset rejection | PASS | same reset with APP_ENV=production | expected nonzero exit before mutation |
| Runtime fixture exclusion | PASS | go list -deps ./cmd/api ./cmd/worker | sessionfixture absent from ordinary executables |
| Presentation/legal routes | PASS | local HTTP reads | six URLs returned 200 |
| Generated-secret scan | PASS | exact-value scan of candidate/tracked source | no runtime password/key/URL values included |
| Live provider writes | SKIPPED | intentionally not invoked | no credentials or external commitments |
| Production rollout/load/CVE certification | NOT VERIFIED | outside this local-cleanup scope | no production-readiness claim |

## Failures encountered and resolved

1. Historical stock-history tests compared serialized-persistence objects using reflect.DeepEqual. A diagnostic established equal complete JSON for the mismatching pair. The helper now compares the full JSON contract, retaining all immutability/rollback assertions. Production calculator/store logic was not changed.
2. During fixture extraction, error responses initially became plain text. Existing integration assertions caught the regression; synthetic fixture now preserves the machine-readable JSON contract. Full integration suite subsequently passed.
3. Lint identified obsolete default-test constants/helpers after cleanup; they were removed. The final make lint passed.
4. Seed development exposed actual schema requirements (nullable/text timestamps, generated import_provider and scoped active-task uniqueness) and a missing query argument. Corrections were made only in fixture code. Failed seeds rolled back, leaving no partial import dataset; a deliberate late-constraint-failure integration test now checks this.
5. A live backup row-count comparison initially observed a discrepancy; verification database was preserved. Counts were subsequently equal. The verifier now canonicalizes row ordering, and the reset workflow stops the owned app before backup. The final reset backup was restored and verified before truncation.

## Existing warnings and limits

Demo builds emit existing third-party use-client and large-chunk warnings; builds return zero. These were not hidden by weakening settings. Source builds and HTTP reads do not prove all demo 3D/third-party map behavior on every device. Browser console-error assertions apply to the real working console.

No integration tests used the working demo database. General regressions used the dedicated local _test DB; seed tests used an independently hosted, empty styk_demo_dev. Production APIs, marketplace mutations and actual customer data were not needed.
