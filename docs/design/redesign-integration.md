# Redesign integration into current master

Reviewed on 5 November 2026 against master `6dae832` and redesign PR #3 (`f5251c1`), including its PR #2 ancestry. The PR design is the presentation baseline. Current master supplies business behavior, tenant permissions and order contracts. PR #1 (editor setup) is outside this integration.

## Compatibility decisions

- Retained the PR sidebar, overview, provider cards, MoySklad settings, product table and matching workspace. Added current orders to desktop/mobile navigation and adapted their header, account context, sections, status colors, controls and typography to the same design tokens.
- Preserved current order configuration, reservations, composition replacement, native documents, unassembled splits, exemplar and country commands, expiring labels, readback, unknown outcomes and recovery. These flows still require their existing separate previews, factual input, approvals and permissions.
- Preserved selection between multiple MoySklad accounts. The older PR assumed one source account; changing the visual layout must not silently choose a different integration pair.
- Removed production shared-key/operator login as requested. `STYK_AUTH_MODE=local`, the key form and operator cookie no longer authorize anything. Merchant email verification, password login, persistent sessions, tenant membership and CSRF remain. Synthetic HTTP fixtures are explicitly named, excluded from the normal API dependency graph, and guarded against non-test executables.
- Kept the current API/worker schema-startup guard. It now requires migration 66 and the connection settings archive column as well as all current order structures.
- Kept review, expiry, conflict and uncertain-confirmation recovery semantics. Simplified presentation does not turn suggestions into confirmed identities or repeat an uncertain external write. The warehouse review explains the current gated rFBS/Express support rather than the older PR's blanket prohibition.

## API and migration implications

`/api/products` retains existing filters and offset pagination, and adds bounded `pageSize`/`page` inputs and exact filtered totals in the existing repeatable-read transaction. `/api/moysklad-products` supports field search, exact totals, bounded pagination and filtering on actually observed positive quantity. Unsupported SKU/activity filters return 422; absent stock stays unknown. Count and page results use the same query snapshot. No provider integration or synchronization algorithm was added.

The PR's migration numbers 45–46 were already used in master. Connection metadata therefore uses **65–66**, leaving every released migration unchanged. Names/descriptions have their own optimistic revision and do not alter encrypted credentials or their revision. Empty settings can roll back to schema 64 and migrate forward again. Rollback refuses to discard saved settings or reactivate archived accounts. Apply migrations and upgrade API, worker and frontend together; development did not migrate production.

CI's dedicated database name now matches the current integration harness allowlist. The 512-test integration package exceeded the previous 20-minute limit; Make and the CI integration race command now allow 40 minutes (`INTEGRATION_TEST_TIMEOUT` is overridable). The timed-out attempts are not counted as passes. Historical PR audit reports, old release claims, screenshots and demo-seeding cleanup were not imported as current evidence.

## Verification

- Go: `golangci-lint run`, `make lint`, `make test`, `go test -race ./...`, `make build` and `go build ./...`.
- Frontend: TypeScript, ESLint, 323 Vitest tests and production build.
- Interface: 27 Playwright checks across desktop/mobile, plus 11 checks against the production build. The seven current sections are checked from 390 to 1920 pixels, including keyboard focus and drawer navigation.
- Real local services: merchant registration/login and orders across 3 API processes, 2 schedulers and 4 workers; process recovery, isolation and unknown-outcome protection.
- Real local API/database: MoySklad metadata edits, credential independence, concurrent revisions, rollback, tenant isolation, and connection/disconnection screens. Product count/filter/pagination compatibility and observed-stock search; real reviewed product confirmation and safe advance to the next product.
- Migration checks: forward migration to 66, empty rollback/reapply, refusal of destructive archived rollback, and rejection of missing required settings structures.

[Desktop orders](screenshots/1440-orders.png), [mobile orders](screenshots/390-orders.png), [products](screenshots/1440-products.png), [matching workspace](screenshots/matching-desktop.png) and [mobile matching](screenshots/matching-mobile.png) capture the integrated UI with synthetic data. The orders shell additionally has its own populated desktop/mobile check.

Final integration-suite result will be recorded before merge. All database and provider verification uses dedicated local databases, synthetic credentials and loopback providers. It does not claim live-provider or production deployment verification.
