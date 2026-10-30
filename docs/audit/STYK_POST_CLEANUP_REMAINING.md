# Remaining problems and next work

| Priority | Component | Cause / consequence | Recommended next step |
|---|---|---|---|
| P0 before commercial production | stocksync/catalogsync/provider contracts | Arbitrary customer publication remains intentionally held | Approved production mutation protocol and rollout; keep fixed test gates |
| P0 before commercial production | hosting/secrets/restore/observability | Local dev processes and DPAPI are not an operational SaaS deployment | Define TLS/proxy, secret manager/rotation, RPO/RTO, restore rehearsals and alerts |
| P1 | billing/legal/onboarding | No paid subscription/entitlements/acceptance lifecycle | Agree narrow MVP and build complete audited paid journey |
| P1 product scope | WB/Yandex/order fulfillment/returns/analytics | Presentation features exceed backend capabilities | Implement only approved vertical slices; do not relabel mocks as working |
| P2 | application.Worker and provider clients | Priority branches and per-client limiter may cause starvation/quota pressure | Fair scheduling and account-scoped limits with measured load |
| P2 | scripts/dev-demo.py, demo-db.py | Windows DPAPI and exact current container assumptions | Portable disposable test environment as separate explicit workflow |
| P2 | internal/sessionfixture and integration adapters | Existing business tests still need synthetic sessions | Keep release dependency exclusion; migrate fixtures only when it improves meaningful coverage |
| P2 | CI | Existing workflow emphasizes Go/DB; new seed test needs separate empty marked instance | Add dedicated seed/TS/browser CI jobs without using working DB |
| P2 | identity model | OWNER-only one-to-one merchant binding; no user display-name field | Explicit product decision before teams/profile changes |
| P3 | sibling demo bundles | Existing large JS chunks / third-party directives | Measure presentation performance before code splitting changes |
| P3 | existing design screenshots | Pre-existing unrelated files remain local/untracked | Owner can publish reviewed assets separately; they were preserved, not deleted |

The earlier seven stock JSON replay test failures are not listed as unresolved: the comparison defect was identified and the final complete integration run passed. Existing immutable history assertions remain.

Local functional readiness does not imply production readiness. No load capacity, universal provider field coverage, billing security or legal conclusion is certified by this cleanup.
