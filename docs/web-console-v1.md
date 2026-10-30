# Web console V1

The console is a local operator tool with explicit product mapping confirmation in the local database. The React/TypeScript Vite application uses React Router and TanStack Query; the Go API queries PostgreSQL and calls the existing matching and Integration Doctor logic. No browser code calls a marketplace, and no screen commits inventory or schedules publication. Account registration is a trusted local administrative assertion, not authenticated tenant authorization.

## Local setup

1. Start the development PostgreSQL instance with `make up`, set `DATABASE_URL` to the local database, and run `make migrate`. Existing registrations and imports are required for real observations; use the normal trusted administrative and import commands documented in the README. Do not use production data for tests.
2. Use the merchant email/password console with configured credential encryption, SMTP verification delivery and STYK_CONSOLE_ORIGIN. The legacy shared-key login has been removed.
3. In another terminal run `cd web && npm ci && npm run dev`, then open `http://127.0.0.1:5173`. Sign in with a verified email address and password.

 The API defaults to loopback. `STYK_ALLOW_REMOTE_BIND=true` explicitly permits a non-loopback listener for health endpoints only; the operator console refuses to start on a non-loopback address even with that opt-in. Use the same loopback origin in the browser and `STYK_CONSOLE_ORIGIN`, including port. The Vite dev server proxies `/api` to the local Go API. The frontend production build is an artifact, not a deployment or a Go-served application.

## Security boundary

Merchant cookies, CSRF checks, workspace membership and roles protect the console. Provider credentials remain server-side and encrypted at rest.


## API and screens

The authenticated GET endpoints are `/api/registrations`, `/api/diagnostics`, `/api/products`, `/api/warehouses`, and `/api/stock`; `/api/session` handles email/password login, session checking and logout. Each data query requires a tenant and, where appropriate, registered source accounts. Failed requests return a machine-readable error code. Queries use the configured database timeout and request cancellation. Product, warehouse and stock pages are bounded to 50 rows; account lists are capped at 100. Matching loads at most 2,000 target items and 2,000 mappings per page, displays at most 20 candidates per item, and reports truncation instead of asserting uniqueness when the universe is too large. The product page requires both accounts. Filters cover search, archived status and confirmed/unconfirmed mapping status.

Overview distinguishes registered sources, completed imports, observation age and unchecked live connectivity. Products show the existing deterministic engine's suggestions; a suggestion is not confirmation. Warehouses show confirmed mappings only for the selected account pair and retain source-specific stock fields. No row means no observation; null means unknown; reported zero remains zero. Observations older than one hour are flagged stale; age is not proof of source freshness. Diagnostics shows Integration Doctor's actual checks and decision status. A missing pool is NOT_CHECKED; UNKNOWN and BLOCKED do not imply zero. READY_CONDITIONAL is never synthesized by the console.

The current account selectors require manually entering an existing tenant. The UI can explicitly confirm product and warehouse mappings through revision-bound reviews, but does not register accounts, trigger imports, manage users or provide stock publication. An operator-attested mapping is not independently verified identity. See [product mapping review](product-mapping-review-v1.md) and [warehouse mapping review](warehouse-mapping-review-v1.md) for audit and recovery limitations.

## Verification and limitations

Run `npm run lint`, `npm run typecheck`, `npm test` and `npm run build` in `web`. The Playwright smoke test (`npm run test:browser`) requires the Go API and Vite running on the default loopback ports and a **disposable** database seeded with `go run ./cmd/doctor -demo -format json`.spec.ts`; never use that secret for a real workspace. Install the browser with `npx playwright install chromium`. The demo inserts synthetic evidence and must not be run against a production database.

The console displays existing database evidence only. It does not establish that APIs are currently reachable, permissions are complete, imports represent atomic snapshots, missing stock is zero, reservations are attributed, or competing exposures are controlled. Browser automation and synthetic demo results are not live integration verification.

A local smoke run against the one-product synthetic fixture measured approximately 1 ms for the product endpoint and 17 ms for diagnostics. These single-run loopback measurements are only a development sanity check, not a capacity or production latency claim.
