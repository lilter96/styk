# Local startup and authentication removal

The Go API and React console now use the existing merchant email/password flow. `STYK_AUTH_MODE=local` is rejected. The shared-key form, key comparison, operator-key environment setting and runtime local-session creation were removed. `/api/session` remains the merchant endpoint; an obsolete `secret` payload is rejected as an unknown field. Merchant cookies, CSRF, email verification, password recovery, membership, roles, encrypted provider credentials and publication gates remain in place.

The retired key lived in process configuration, with in-memory sessions. No dedicated key table or persisted key column was found; no destructive migration is needed. Existing migrations remain unchanged.

Existing business tests use synthetic session fixtures: the default fixture is in a `_test.go` file; the integration fixture requires the integration build tag and a Go test executable. Neither validates a shared login key. These fixtures are absent from ordinary API and worker builds. The merchant integration regression checks that obsolete login payloads fail, anonymous business reads fail, and verified email/password login still succeeds.

## Running local instance

- Frontend: http://127.0.0.1:5173
- API liveness/readiness: http://127.0.0.1:8080/health/live and /health/ready
- Local mail: http://127.0.0.1:8025
- PostgreSQL: existing `styk-redesign-db` container, loopback port 55433, database `styk_ui_redesign`.
- API and worker: `.local/redesign-api.exe` and `.local/redesign-worker.exe`; logs and PID files use `.local/local-*`.
- Local sign-in details: ignored `.local/local-login.txt`.

The existing Windows DPAPI-protected encryption key was reused. The local database was empty; existing migrations through version 44 were applied. A separate `styk_auth_test` database is used for integration checks. No provider mutation or stock publication was performed.

Verification includes a real local registration email delivered to Mailpit, email verification, successful password login, authenticated session readback, anonymous rejection and obsolete-key rejection. Frontend typecheck, ESLint, tests and production build passed. Go unit tests, race tests, golangci-lint and builds passed in Go 1.27.3 Docker tooling. Direct `make lint` reports pre-existing formatting differences across the Windows checkout; unrelated files were not reformatted.

Full `make test-integration` ran on the dedicated database and reported seven stock commitment history/replay failures. `TestStockImportedEvidenceIsUnknownAndImmutable` was reproduced from an unchanged HEAD archive, confirming that failure predates this change. The new merchant authentication regression passed. A mock demo outbox task was consumed by the running worker and reached `succeeded`.
