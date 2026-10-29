# Conditional stock publication V1

> **Safety hold (audit 2026-10-29): real marketplace writes are disabled in code, including when the deployment flag is set.** The feature is available only to explicitly configured literal-loopback simulators, with synthetic credentials. Existing enabled settings do not make the worker send to Ozon. See [the safety audit](stock-publication-safety-audit.md) before considering live use.

This adds an explicitly enabled, scoped MoySklad → Ozon stock publisher. It does not turn a catalog import or a confirmed product mapping into a publication guarantee. The existing calculator and legacy `stock_decisions` remain fail-closed for imported evidence without critical proofs; their immutable UNKNOWN/BLOCKED history is preserved.

## Readiness and explicit controls

`GET /api/stock-sync?tenant=…&moysklad_account=…&ozon_account=…` is read-only. It returns the current setting, human-checkable readiness, bounded targets and latest durable operation. Both accounts must be registered to the exact tenant. At most 100 confirmed product–warehouse pairs are considered. Targets freeze product/warehouse mapping IDs and versions, source/Ozon import IDs and observations, credential revisions, policy, conditional quantity, proof provenance/validity and source order evidence.

Ordinary imports lack critical cross-system guarantees and therefore cannot enable publication. `POST /api/stock-sync/preview` can record explicit, scoped, operator-supplied evidence. The application does not manufacture it, fill missing quantities with zero or check its acknowledgement boxes automatically. Each target requires explicit nonnegative whole-piece quantities for unreflected obligations, unseen reductions and safety stock, plus these named controls:

- `SALE_UNIT_UNVERIFIED`: one whole piece, simple composition and no unit conversion.
- `SOURCE_BASELINE_NOT_PROVEN`: consistent source baseline and complete visibility.
- `SOURCE_ORDERING_UNPROVEN`: source ordering/watermark and freshness boundary.
- `UNASSIGNED_RESERVE_COVERAGE_MISSING`: warehouse-unassigned reserves covered.
- `RESERVATION_ATTRIBUTION_UNKNOWN`: source/channel reservation overlap reconciled.
- `POSTING_COVERAGE_INSUFFICIENT`: complete Ozon obligations, including pre-posting reservations.
- `COMPETING_EXPOSURE_NOT_VERIFIED`: exclusive control and competing exposure established.
- `UNSEEN_REDUCTION_BOUND_UNPROVEN`: a disjoint bound for unseen reductions.
- `CUSTOMER_ORDER_COVERAGE_VERIFIED`: complete customer-order/reservation coverage and its inclusion in the source baseline explicitly established, including independent/manual evidence when an imported order book is absent.

The ninth control is additive: no existing importer supplies it and the legacy calculator stays UNKNOWN by default. It can supply missing independent order coverage/baseline inclusion; it does not override conflicting/ambiguous identities, truncated evidence, unscoped detail, invalid/negative numeric reserves or any existing source/account/mapping conflict. Provenance is stored as `operator-attested`, not labelled automatically or independently verified by the software.

Every proof needs meaningful provenance (16–1000 characters), `as_of` no later than the start of the frozen source traversal for dynamic conditions and validity bounded to fifteen minutes from that check. No proof is valid in the future or indefinitely. Source and Ozon stock observations must be present and complete; both traversals must have begun less than fifteen minutes ago and their observations must also be fresh. Review lifetime is at most two minutes and is shortened by either traversal or proof expiry. Quantities must fit a nonnegative whole-piece Ozon update; unsupported identities/quantities block publication. A reported zero is valid evidence; a missing value remains unknown.

### Preview request

```json
{
  "tenant": "workspace",
  "moysklad_account": "source-account",
  "ozon_account": "seller-account",
  "attestations": [{
    "ozon_product_id": "123",
    "ozon_warehouse_id": "456",
    "unreflected": "0",
    "unseen_bound": "2",
    "safety_stock": "1",
    "proofs": [{
      "code": "CUSTOMER_ORDER_COVERAGE_VERIFIED",
      "reference": "Reference to the checked, scoped complete order/reserve reconciliation",
      "as_of": "2026-10-29T06:35:00Z",
      "valid_until": "2026-10-29T06:40:00Z"
    }]
  }]
}
```

This example deliberately contains one proof and is not sufficient for READY. Omitting `attestations` produces a useful blocked preview without inventing evidence.

Preview response: `{ review_id, tenant, moysklad_account, ozon_account, ready, checks, targets, created_at, expires_at }`. Each check is `{ code, passed, message }`; each target is `{ ozon_product_id, ozon_warehouse_id, source_item_id, source_warehouse_id, product_mapping_version, warehouse_mapping_version, source_import_id?, quantity?, status, checks, settlement_operation_id?, settlement_observed_at? }`. Settlement fields identify a previously uncertain writer that requires explicit review. Nested frozen calculator/evidence snapshots and proof input are stored durably but removed from public views.

## Enable, stop and safe recovery

`POST /api/stock-sync/enable` is currently available only with the explicit loopback simulator configuration; otherwise it returns `STOCK_PUBLICATION_DISABLED`. In simulator mode it requires the exact scope, ready `review_id`, a client-generated `idempotency_key` and `acknowledge:true`. The server locks/rechecks current versions, credentials, scope, values and expiry before atomically enabling and queuing the frozen operation. A repeated identical intent returns the existing scope status without another operation. Reusing a key for a different action/review returns `IDEMPOTENCY_CONFLICT`. An already enabled setting can be renewed only with a new explicit review and no unresolved queued/in-flight/unknown target; reusing an old review with a new key is blocked.

`POST /api/stock-sync/disable` requires the same scope, a distinct idempotency key and explicit acknowledgement. It cancels queued targets and prevents future dispatches. An already dispatched request may still complete; the API reports STOPPING rather than claiming it was undone. Unknown remote outcomes remain visible after disabling.

All new mutation routes reuse the existing authenticated local-operator, same-origin JSON boundary. This remains a trusted local operator interface, not production tenant authentication. HTTP errors expose safe codes only: `INVALID_SCOPE`, `INVALID_REQUEST`, `STOCK_SYNC_NOT_FOUND`, `STOCK_SYNC_BLOCKED`, `STOCK_SYNC_STALE`, `IDEMPOTENCY_CONFLICT`, `STOCK_SYNC_UNAVAILABLE`.

## Worker and external API boundary

`stocksync.Runner.RunNext` is called separately from the import/mock queue, and is a no-op unless the simulator-only deployment gate is enabled. A transaction marks a target DISPATCHING before network I/O; no transaction is held across an external call. That state is never reclaimed for another write. Interrupted dispatches become UNKNOWN after a minute. The simulator worker constructs a literal-loopback client using synthetic credentials; it does not resolve or send marketplace keys. The explicit readback route still resolves scoped stored credentials or an exact registered environment fallback and performs read-only provider requests. Revision changes or a disconnected stored credential block frozen work.

The synthetic loopback endpoint receives at most one bounded `POST /v2/products/stocks` attempt, with product ID, warehouse ID and the frozen available quantity. Read-client retry settings are not used for writes. Redirects, transport loss, malformed/missing target results and server errors become uncertain rather than automatic retries. Successful `updated` results are stored per target. All non-2xx responses and individual errors remain UNKNOWN until provider guarantees are verified; partial uncertainty remains explicit. Only allowlisted safe provider codes are retained; raw responses, keys and headers are not persisted or returned.

A global durable target fence spans every account-pair scope for the same Ozon account/product/warehouse while work is queued, dispatched or unknown. Database dispatch clocks allow at most one request per second per account and one update per target per thirty seconds. Other accounts are not stalled behind a rate-limited target.

These choices follow Ozon's [stock-management recommendations](https://dev.ozon.ru/start/299-Rekomendatsii-po-upravleniiu-ostatkami-v-Seller-API/): persist changes before sending, use `/v2/products/stocks`, bound batches to 100 pairs, avoid unchanged periodic rewrites and respect per-account/per-target limits. Ozon's [official FAQ](https://dev.ozon.ru/start/257-FAQ-spravochnik-oshibok-i-sposoby-ikh-resheniia) explains that submitted free stock is added to existing reservations; the publisher sends the calculator's available quantity without adding reserve again. Legacy `/v1/product/import/stocks` is [retired](https://dev.ozon.ru/news/585-Seller-API-Otkliuchenie-metoda-v1-product-import-stocks/).

While enabled, the worker reevaluates at most five scopes per pass, no more often than every five seconds. Only new eligible persisted evidence with changed proposed quantities and still-valid original scoped controls can enqueue another bounded publication. It does not schedule imports, blindly repeat an identical quantity or automatically bypass expired proofs/mapping/credential changes. Uncertain targets fence subsequent work until explicit reconciliation.

`POST /api/stock-sync/operations/{id}/reconcile` requires scope and `acknowledge:true`. It performs only bounded Ozon stock reads. A read response matching the frozen free quantity records `CONFIRMED_EFFECTIVE` as a reported match; neither freshness of the read nor causal execution of the original request is established. A mismatch/unavailable read remains uncertain and never triggers a rewrite.

A matching readback atomically disables automatic publication and returns `EFFECT_CONFIRMED_REVIEW_REQUIRED`. It does not set `last_confirmed_at` or resume on the next source import. Before a new explicit review can enable publication, the competing-writer control must attest that the original uncertain writer has settled and no outstanding write remains. Its provenance must include the original `settlement_operation_id`, and its `as_of` must be strictly later than `settlement_observed_at`. Since proof time must also precede the source observation, a new source observation after that settlement assertion is required. This is an explicit operator-attested settlement, not independently established platform execution evidence. The historical guard applies to the physical Ozon target across all MoySklad account scopes. Other still-unknown targets continue to fence writes; a new review never clears them.

## Public status

View: `{ tenant, moysklad_account, ozon_account, enabled, state, ready, checks, targets, last_operation?, last_confirmed_at? }`. `enabled` records operator intent, not successful dispatch; UI must prioritize `state` and current readiness over an older successful operation. A proof expiry therefore shows BLOCKED even when the last request succeeded. Operation: `{ id, review_id, state, created_at, targets }`; target: `{ id, operation_id, ozon_product_id, ozon_warehouse_id, quantity, state, safe_error?, dispatched_at?, finished_at? }`. Public processing states use `RUNNING` and `OUTCOME_UNKNOWN`; the specialized internal states remain DISPATCHING/UNKNOWN.

## Verification

Twenty-eight synthetic publication integration cases cover missing proofs; immutable review/quantity scope, exact idempotency and concurrent single claims; global target fences across MoySklad scopes; queued disable and in-flight STOPPING; one-attempt unknown results with no automatic replay; explicit readback that disables automatic publication and requires fresh scoped settlement evidence; partial success/readback; fresh changed evidence auto-queue without identical loops; stale source/proof refusal; and credential revision changes requiring a new explicit review. One case exercises the real authenticated console handler over loopback HTTP against the dedicated database: login/cookie, scoped preview, explicit enable, queued status, identical retry, changed-intent conflict and queued cancellation. It never invokes a provider or worker. Calculator and adapter tests additionally cover invalid/expired customer-order proofs, preserved conflicts and unsafe numeric data; payload identity/zero, validation before I/O, safe error codes and incomplete/duplicate result handling. Auth tests ensure session, origin and explicit acknowledgement precede database/provider use. See the [audit](stock-publication-safety-audit.md) for additional adversarial cases and limitations.

Use a dedicated migrated local database named `styk_stock_publication_v1_test`:

```sh
TEST_DATABASE_URL='<dedicated local database URL>' \
  go test -race -tags=integration -count=1 -run '^TestStockPublication' ./tests/integration
```

Migration 24 adds publication settings/reviews/targets/intent history/fences. Down migration refuses to discard any review or publication evidence. No live marketplace API is used by verification.
