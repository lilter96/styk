# Project guidance

STYK is a modular monolith: domain types carry business meaning, application orchestrates work, postgres owns SQL and transactions, and transport exposes only health endpoints. Keep API and worker independent entry points; do not add generic repositories or premature services.

Use idiomatic Go, contexts, wrapped errors, structured logs without secrets, bounded work and explicit transaction boundaries. Never keep a transaction open across an external API call. Keep dependencies minimal, maintained, and justified in review; use the standard library where practical.

Run `make lint`, `make test`, `go test -race ./...`, and `make build`. For database changes also run `make migrate` and `make test-integration` against a dedicated local test database. Tests must assert behavior, including rollback, concurrency and failure; never use production services. Review migrations for forward and rollback impact and never apply them to production as part of development.

External writes must not be added without an explicit design for idempotency, unknown outcomes, retries, reconciliation and partial success. Never log tokens, connection strings, customer data or credentials. The mock worker's lease recovery is not safe for arbitrary writes.

AI-assisted changes must inspect existing work, preserve unrelated modifications, verify claims with actual commands, disclose unrun checks, and avoid inventing integrations or business rules. Done means code, tests, documentation and migration implications reviewed, relevant checks run, and limitations reported.

## Owner-authorized test accounts

The owner has authorized autonomous development and controlled real-provider mutations in these two test accounts, currently registered in the `cross-e2e` workspace, identified by SHA-256 of their account IDs:

- MoySklad: `8fd183ebcacb9d3526ac269f4c83417147347a4292f827dc0372a6960ff7bbfd`.
- Ozon: `a3a518ffbc984fe17a51866b29463c58a6dbec607e32d754419322c9e5489a1d`.

Verify the exact account and tenant before using this authority. It does not extend to other accounts, customer payments, real shipment dispatch, refunds, or actions outside the authorized development experiment. Within these scopes, ordinary controlled test work may create, modify, reuse, or archive products and variants; adjust test inventory and prices; create test inventory movements, orders, and reservations; confirm reviewed STYK mappings; and perform bounded real-provider API and end-to-end synchronization experiments. Do not ask for separate approval for each ordinary mutation already covered by the user's task.

These are real provider accounts, not necessarily provider-isolated sandboxes. The owner accepts ordinary operational risks of testing and expressly accepted possible real-buyer exposure for the bounded 0 → 1 → 0 experiment on Ozon offer `ATEST-897677`, product `2764544165`, warehouse `1020001323428000`. Do not infer or claim buyer isolation. Honor the user's explicit target, quantity and risk constraints, record any buyer-exposure risk and real external commitments honestly, and do not impose unsupported demands for absolute certainty. A provider restriction, reproducible defect, or unresolved external outcome remains a blocker when it materially affects the operation.

Preserve account and tenant isolation, transactional invariants, durable mutation intents, audit records, rate limits, concurrency and recovery guarantees, and deterministic tests. Journal external writes before dispatch; use provider idempotency where supported, inspect per-target responses, reconcile ambiguous outcomes, and never blindly resend an uncertain write or represent it as successful. Keep diagnostics isolated from normal application and browser execution, narrowly allowlisted and bounded. Preserve existing evidence and do not expose credentials or customer data.

This authorization does not enable stock publication for other customers. The normal real-host production publication hold remains enabled until a separately approved production implementation and rollout. Test-only experiments must not weaken the production API, worker, adapter, or global safety gate. Prefer completing authorized vertical slices, including real provider reads, durable state, reviewed mappings, bounded writes, readback and reconciliation where applicable.
