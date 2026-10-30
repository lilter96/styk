# Security review

## Confirmed safeguards

- New demo DB rather than clearing an unknown existing database.
- Exact local image/container/loopback mapping, database comment and sole synthetic owner/account checks.
- Real backup archive, SHA-256, restore and table/row comparison before reset.
- Schema/Goose history preserved; no production migration changed.
- Development/URL/query-override guards, transaction advisory lock and atomic seed commit.
- Zero connection_credentials; all account identifiers explicitly STYK-DEMO-prefixed.
- Verified email/password registration through normal local API and Mailpit; existing bcrypt/session logic.
- Long random password; current-user Windows DPAPI storage and one-time local delivery.
- Dedicated non-superuser local runtime role; no production connection strings in reports.
- Normal API/worker exclude sessionfixture; operator-cookie runtime fallback removed.
- HttpOnly/SameSite Strict cookie; CSRF and tenant isolation checks preserved and smoke-tested.
- Worker received no provider credentials/publication switches; only safe mock tasks consumed.
- .local, outputs, Python caches and ZIP files excluded from publication/build context as appropriate.
- Exact runtime password/key/URL scan found no matches in candidate/tracked source.
- .env, DPAPI files, SQL dumps, user passwords and private keys are excluded from PR and audit archive.

## Remaining risks

| Priority | Risk | Consequence / recommendation |
|---|---|---|
| P0 before production | General external mutation safety/rollout not approved | Preserve gates; design idempotency/unknown/partial success/reconcile and staged activation |
| P0 before production | Secrets/backup/restore operational model | Local DPAPI is not a production secret manager; define rotation, recovery and access separation |
| P1 | Application-level tenant isolation without RLS | Keep route/SQL/actor guards and cross-tenant regression tests; review new endpoints |
| P2 | Integration-tag synthetic test scaffolding still exists | Keep it out of release artifacts and normal bootstrap; test-only helpers are not real authentication |
| P2 | Windows/fixed-local-instance helper assumptions | Do not repoint it to arbitrary hosts; build a separate portable disposable CI environment deliberately |
| P2 | DB outbox fairness / instance-local provider limiter | Measure backlog and shared account quotas before scaling |
| P3 | Third-party demo/fonts/maps and dependency advisory review | Separate product/privacy/SCA review; no current CVE certification claimed |

No confirmed new production vulnerability or production data mutation was found/performed in this task. This statement is bounded to the implemented local workflow and checks, not an exhaustive penetration test.
