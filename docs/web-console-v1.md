# STYK console access and verification

The current console uses merchant accounts with email verification and password login. Shared operator-key login and `STYK_AUTH_MODE=local` have been removed. The original V1 operator workflow is retired.

## Local startup

Apply all migrations with `make migrate`. Configure `DATABASE_URL`, `STYK_AUTH_MODE=merchant`, `STYK_CONSOLE_ORIGIN`, `STYK_CREDENTIAL_ENCRYPTION_KEY` and SMTP delivery as described in [merchant identity](saas-identity-plan.md). Start `go run ./cmd/api`, then run `npm ci` and `npm run dev` from `web`. Use registration and the verification email to create an account. Missing security configuration fails closed. `STYK_AUTH_MODE=disabled` exposes health endpoints without a console.

The frontend uses the design from PRs #2 and #3 with current master workflows, including orders. Account selection is explicit; choosing one account does not authorize another tenant. The session cookie is HttpOnly, with server-side membership and permission checks. CSRF and same-origin controls remain enabled. Provider credentials are never returned to the browser.

The product and MoySklad search endpoints retain legacy offset pagination and add bounded page sizes and exact filtered totals. Product identity still requires a current review and explicit confirmation. Imports do not establish source freshness or zero stock for absent rows. Order previews, approvals, unknown-outcome fences and readback remain authoritative.

## Verification

Run `npm run typecheck`, `npm run lint`, `npm test` and `npm run build` in `web`. `npm run test:browser` owns an isolated API/worker/provider stack and requires a dedicated localhost database named `styk_orders_release_test` or `styk_orders_store_test`; see [the README](../README.md#merchant-and-order-browser-verification). It exercises real merchant registration and login. It never uses production services or live provider credentials.

Older HTTP tests use a separately named synthetic fixture cookie. Their handler adapters compile only with the `integration` tag (or in unit-test files), and the fixture implementation rejects non-test executables. They are not reachable from normal API startup and do not restore operator-key authentication.

See [the redesign integration review](design/redesign-integration.md) for current evidence and migration implications. Browser fixtures and loopback provider tests are not verification of live provider behavior.
