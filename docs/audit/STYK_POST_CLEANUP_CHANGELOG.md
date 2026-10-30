# Change log

Baseline preservation: `d919088` records pre-existing working-tree UI/auth/audit work. The table below describes cleanup/setup changes relative to that checkpoint, rather than claiming earlier work as new cleanup.

| File | Action | Reason | Consequences |
|---|---|---|---|
| `docs/audit/STYK_POST_CLEANUP_CHANGELOG.md` | new | File/action/commit provenance | No credentials or source copied to the audit ZIP |
| `.dockerignore` | modified | Exclude local secrets/caches/build outputs from build context | No runtime capability change |
| `.gitattributes` | new | LF for Go/shell files | Avoid CRLF-only gofmt failures |
| `.gitignore` | modified | Exclude generated audit ZIP and Python cache | Private/generated artifacts stay untracked |
| `Makefile` | modified | Lint maintained source trees, excluding .local/node_modules | No weakening of actual source lint rules |
| `README.md` | modified | Link development workflow/current handoff | Existing documentation retained |
| `cmd/demo-seed/main.go` | new | Explicit guarded development seed command | Never automatic startup |
| `docs/audit/00_EXECUTIVE_SUMMARY.md` | modified | Mark earlier audit as historical | Link current post-cleanup facts |
| `docs/audit/STYK_POST_CLEANUP_DATABASE.md` | new | Development documentation / technical handoff | No secrets or production configuration |
| `docs/audit/STYK_POST_CLEANUP_HANDOFF.md` | new | Development documentation / technical handoff | No secrets or production configuration |
| `docs/audit/STYK_POST_CLEANUP_INFRASTRUCTURE.md` | new | Development documentation / technical handoff | No secrets or production configuration |
| `docs/audit/STYK_POST_CLEANUP_PR.md` | new | Development documentation / technical handoff | No secrets or production configuration |
| `docs/audit/STYK_POST_CLEANUP_REMAINING.md` | new | Development documentation / technical handoff | No secrets or production configuration |
| `docs/audit/STYK_POST_CLEANUP_REPORT.json` | new | Development documentation / technical handoff | No secrets or production configuration |
| `docs/audit/STYK_POST_CLEANUP_REPOSITORY.md` | new | Development documentation / technical handoff | No secrets or production configuration |
| `docs/audit/STYK_POST_CLEANUP_SECURITY.md` | new | Development documentation / technical handoff | No secrets or production configuration |
| `docs/audit/STYK_POST_CLEANUP_TESTING.md` | new | Development documentation / technical handoff | No secrets or production configuration |
| `docs/development/DEMO_DATA.md` | new | Development documentation / technical handoff | No secrets or production configuration |
| `go.mod` | modified | Promote used x/crypto to direct requirement after tidy | Versions unchanged; no dependency removed |
| `internal/console/auth.go` | modified | Remove unused runtime operator session implementation | Merchant delegation and fail-closed default remain |
| `internal/console/auth_test.go` | modified | Assert retired cookie cannot authenticate | Negative auth regression preserved |
| `internal/console/merchant_auth.go` | modified | Remove unused outer-auth origin assignment | Merchant origin/CSRF behavior retained |
| `internal/console/session_fixture_integration.go` | modified | Delegate synthetic sessions to shared test support | Integration fixture behavior remains machine-readable |
| `internal/console/session_fixture_test.go` | modified | Trim duplicate/unused default-test handlers | Used unit session adapter remains |
| `internal/devseed/seed.go` | new | Atomic deterministic guarded fixture loader/count checks | Marked local DB and sole verified demo user required |
| `internal/devseed/seed_integration_test.go` | new | Real DB rollback/concurrency/isolation assertions | Dedicated disposable instance only |
| `internal/devseed/seed_test.go` | new | Development boundary tests | Remote/production/override inputs rejected |
| `internal/devseed/sql/catalog.sql` | new | Synthetic fixture values and scoped relationships | Development-only; existing schema |
| `internal/devseed/sql/connections.sql` | new | Synthetic fixture values and scoped relationships | Development-only; existing schema |
| `internal/devseed/sql/events.sql` | new | Synthetic fixture values and scoped relationships | Development-only; existing schema |
| `internal/devseed/sql/mappings.sql` | new | Synthetic fixture values and scoped relationships | Development-only; existing schema |
| `internal/devseed/sql/orders.sql` | new | Synthetic fixture values and scoped relationships | Development-only; existing schema |
| `internal/devseed/sql/products.sql` | new | Synthetic fixture values and scoped relationships | Development-only; existing schema |
| `internal/sessionfixture/auth.go` | new | One synthetic session implementation outside normal runtime deps | Test-only constructor; distinct cookie |
| `scripts/demo-db.py` | new | Prepare/backup/restore/reset/verify exact demo target | No unknown DB/volume cleanup; Goose preserved |
| `scripts/dev-demo.py` | new | DPAPI local lifecycle and ordinary registration/smoke | No plaintext credentials or provider gates |
| `tests/integration/stock_commitment_test.go` | modified | Compare full persisted JSON instead of reflect internals | No calculator/store logic change |
| `web/e2e/demo-dev.spec.ts` | new | Actual local protected pages and auth boundary tests | Password supplied transiently through environment |

## Removed / retained

Tracked files deleted: **0**. Dependencies removed: **0**. No deletion was justified for used modules, dynamic imports, future maintained integrations, assets, legal pages or migration history. Removed code is the unused operator-runtime implementation and duplicate/default-test helpers described above. Two obsolete ignored local files were removed; their plaintext password was not copied to source or reports. Existing keys/evidence/screenshots were preserved.

## Commit checkpoint

| Hash | Subject |
|---|---|
| `d919088fc9cfd34bbbadaee247dde0444c536695` | chore: preserve pre-cleanup working tree |
| `01d76b6fb730a4713aec44c4078446ef79702a22` | refactor: remove unused code and dependencies |
| `dc7d6da308125602d5e9a859891f2fcfe639b738` | refactor: isolate and trim synthetic session adapters |
| `93740f42d83bc4831d2c1e8212e4a195198e6fcf` | chore: add safe development database reset |
| `9befba3fb5cc5ff11ce767d85bc5738673014456` | feat: add reproducible demo data seeding |
| `a51ce98671bbca79eaca755908b9c3764f5aeda8` | test: verify demo account and data integrity |
| `cd41eb3ecba13b6d024891fe0f1a3b680cef4481` | test: validate development foreign keys after seeding |

This list records the code checkpoint before the reporting commit; the open PR contains the final documentation commits as well. No self-referential commit hash is fabricated.

Documentation checkpoint: `2d51f39` ? setup/handoff and generated-cache exclusions. Actual review: [PR #2](https://github.com/lilter96/styk/pull/2). The following link-recording documentation commit is discoverable in the PR history; this file does not invent its own future hash.
