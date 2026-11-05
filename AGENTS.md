# Go Development Guidelines

## Commands & Verification
Use the executable policy in [docs/testing-policy.md](docs/testing-policy.md).
- Inspect existing work and run `make check-plan` before choosing checks.
- Run `make check` for the changed scope: Go lint, race-enabled unit tests and
  build for affected packages and their transitive consumers; frontend checks
  only when frontend files changed. Documentation has no application test suite.
- Run `make check-ui` for selected UI behavior/layout changes, and
  `make check-integration` for selected database/contracts. These targets select
  relevant tests; they do not run the entire integration suite. PostgreSQL is
  disposable unless a validated dedicated loopback test DSN is supplied.
- Full regression (`make check-full`) belongs to nightly/manual CI and changes
  classified as high risk (schema, shared security/dispatch/dependencies or
  unknown impact). Do not start it for routine edits or as a push prerequisite.
- Reuse already-passed checks for unchanged content. A commit, push, docs-only
  follow-up, or request for status does not invalidate prior verification.
- Never run both ordinary and race versions of the same suite by default.
  If a broader suite is needed, run it once with `-race`.
- Report exactly which selected checks passed, failed, were skipped or stopped.
  A stopped or unrun full suite must never be reported as passed.

## Code Style & Architecture
- **Keep happy path left-aligned**: Return early, avoid nested `if-else` chains.
- **Function size**: Keep functions under 40 lines. Split complex workflows into private helper steps.
- **Domain logic**: Keep business conditions inside domain methods (e.g., `pool.IsValid()`), not multi-line boolean chains.
- **Packages**: Use lowercase, single-word names. Never create `util`, `common`, or `helpers`.
- **Layout**: Put private app code in `internal/`, entry points in `cmd/`.

## Interfaces & Composition
- **Accept interfaces, return concrete types** (structs or pointers to structs).
- Define small, consumer-side interfaces (1–3 methods, `-er` suffix).
- Avoid premature abstractions or mock wrappers if standard types suffice.

## Error Handling
- Never ignore errors: always check `if err != nil`.
- Wrap errors with context: `fmt.Errorf("action description: %w", err)`.
- Inspect errors with `errors.Is` and `errors.As`; never compare error strings (`err.Error() == "..."`).
- Declare sentinel errors at package level: `var ErrNotFound = errors.New(...)`.
- Never use `panic` in business/library logic.

## Concurrency & Context
- Pass `ctx context.Context` as the first argument in all I/O, database, and blocking functions.
- Never store `context.Context` inside a struct.
- Every goroutine must have an explicit exit guarantee via `context`, `sync.WaitGroup`, or `errgroup`.
- Share memory by communicating (channels); do not communicate by sharing memory.

## Testing
- Use standard table-driven tests (`tests := []struct{ name string, ... }`).
- Execute subtests using `t.Run(tc.name, func(t *testing.T) { ... })`.
- Call `t.Parallel()` in unit tests that do not share mutable state.

## Memory & Types
- Preallocate slices and maps when capacity is predictable: `make([]T, 0, cap)`.
- Use `strings.Builder` for multi-step string building.
- Prefer standard `int64` / `float64`; do not introduce `math/big` unless explicitly required by spec.

# Project guidance

STYK is a modular monolith: domain types carry business meaning, application orchestrates work, postgres owns SQL and transactions, and transport exposes only health endpoints. Keep API and worker independent entry points; do not add generic repositories or premature services.

Use idiomatic Go, contexts, wrapped errors, structured logs without secrets, bounded work and explicit transaction boundaries. Never keep a transaction open across an external API call. Keep dependencies minimal, maintained, and justified in review; use the standard library where practical.

Select verification using `make check-plan` and the scoped targets above. For database changes run the selected contract tests against a dedicated local test database; schema migrations require the full CI lane and forward/rollback review. Tests must assert behavior, including rollback, concurrency and failure; never use production services or apply development migrations to production.

External writes must not be added without an explicit design for idempotency, unknown outcomes, retries, reconciliation and partial success. Never log tokens, connection strings, customer data or credentials. The mock worker's lease recovery is not safe for arbitrary writes.

AI-assisted changes must inspect existing work, preserve unrelated modifications, verify claims with actual commands, disclose unrun checks, and avoid inventing integrations or business rules. Done means code, tests, documentation and migration implications reviewed, relevant checks run, and limitations reported.
