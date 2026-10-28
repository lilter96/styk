# Project guidance

STYK is a modular monolith: domain types carry business meaning, application orchestrates work, postgres owns SQL and transactions, and transport exposes only health endpoints. Keep API and worker independent entry points; do not add generic repositories or premature services.

Use idiomatic Go, contexts, wrapped errors, structured logs without secrets, bounded work and explicit transaction boundaries. Never keep a transaction open across an external API call. Keep dependencies minimal, maintained, and justified in review; use the standard library where practical.

Run `make lint`, `make test`, `go test -race ./...`, and `make build`. For database changes also run `make migrate` and `make test-integration` against a dedicated local test database. Tests must assert behavior, including rollback, concurrency and failure; never use production services. Review migrations for forward and rollback impact and never apply them to production as part of development.

External writes must not be added without an explicit design for idempotency, unknown outcomes, retries, reconciliation and partial success. Never log tokens, connection strings, customer data or credentials. The mock worker's lease recovery is not safe for arbitrary writes.

AI-assisted changes must inspect existing work, preserve unrelated modifications, verify claims with actual commands, disclose unrun checks, and avoid inventing integrations or business rules. Done means code, tests, documentation and migration implications reviewed, relevant checks run, and limitations reported.
