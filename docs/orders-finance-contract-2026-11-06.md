# Monthly realization: verified read and accounting boundary

Checked on **2026-11-06** against the current [published Seller OpenAPI](https://docs.ozon.ru/api/seller/zh/swagger.json), OpenAPI 3.0.0, `info.version: 2.1`. The source was accessible through the web reader; direct `curl` failed TLS negotiation, so this change does **not** claim a downloaded Swagger checksum. The [versioned contract record and synthetic fixture](../internal/infrastructure/ozon/testdata/realization_20261106/contract.json) identify the reviewed schemas and fixture digest.

`POST /v2/finance/realization` accepts required integer `month` and `year` and returns `result.header` / `result.rows`. `Client-Id` and `Api-Key` authenticate the request. The HTTP method is explicitly registered as a read in STYK's existing provider coordinator. The adapter uses one attempt, a 20-second request timeout, a 4 MiB response limit and a 10,000-row local preview limit. These are STYK bounds, not Ozon's advertised limits. Oversized responses are rejected without returning partial rows.

The native provider response has no mandatory response fields in its schema. STYK requires an explicit rows array, report number, currency, exact SKU and offer identity and unique integer `rowNumber` before returning the review. V2 uses **`rowNumber`**, not the posting report's `row_number`. Missing, explicit null and numeric zero remain different facts. Money stays exact decimal text, including negative fees and fractional minor units; binary floating-point conversion and automatic rounding are absent. Unsupported exponent representations, malformed numbers, duplicate keys, unknown fields and ambiguous envelopes fail closed.

The public endpoint is `GET /api/orders/finance/realization?ozon_account=…&moysklad_account=…&year=2026&month=9`. Merchant authentication supplies the tenant. Existing ownership, enabled source reading, configuration revision and credential revision are checked before the request, after provider admission waiting, and before returning the report. The response is `Cache-Control: no-store`; payer/receiver names and tax identifiers are discarded. It contains source rows and a content fingerprint, never a native accounting success or inferred payment.

## Source availability and conflicting publications

The current schema allows reports from August 2023 and describes publication by the fifth day of the following month. [The older May 2024 announcement](https://dev.ozon.ru/news/333-Novaia-versiia-metoda-otcheta-o-realizatsii-v-Seller-API/) says after the fifth. STYK follows the current contract and requires a real report response; a calendar date alone proves neither publication nor completeness. Current and future accounting months are not accepted by this preview. Authentication/permission errors, missing source, rate limits and provider outages remain distinct safe diagnostics. A 404 is not fabricated into an empty or unpublished report.

[The finance migration notice](https://dev.ozon.ru/news/783-Izmeneniia-v-metodakh-dlia-raboty-s-finansovymi-otchetami-v-Seller-API/) retires transaction list/totals on 2026-09-08, names accrual replacements and recommends sequential requests. This change calls neither the retired endpoints nor accrual analytics; accruals do not replace the realization document.

## What the review does and does not establish

The fingerprint identifies the requested scope/period, source report number, currency and sanitized financial contents, independent of observation time and private header details. Reordered rows do not change it. A changed fee or quantity changes the fingerprint. The preview does not persist an import or silently update a previously applied report.

`delivery_commission` and `return_commission` expose `quantity`, `amount`, `bonus`, `price_per_instance`, `standard_fee`, `bank_coinvestment`, `stars` and `total`, plus legacy `commission` and `compensation`. The legacy pair applies only to reports generated through 2024-04-30. All known financial components are retained in the sanitized source snapshot. Header payer/receiver identifiers, names and product descriptions/barcodes are deliberately discarded; this is not a raw lossless report archive.

The [official MoySklad Ozon import benchmark](https://support.moysklad.ru/hc/ru/articles/13981082723857-Полученный-отчет-комиссионера-из-Ozon) defines commission as base reward minus discount points and allows a negative commission. STYK maps the corresponding current API fields as `delivery.standard_fee − delivery.bonus`, preserving the sign. The benchmark does not establish a payout, profit, payment or a universal mapping of all financial components. Other services require separate source documents and accounting review.

The source preview still exposes caveats for accounting review, absence of declared source totals/row count, and absence of FBS/FBO scheme and posting correlation on V2 rows. These are source facts, not an assertion that every source report is unimportable. The native planner independently validates the supported subset. An unverified calendar interval adds `report_header_period_not_verified`; received row count is never an independently reconciled provider total.

`POST /v1/finance/realization/posting` is a separately verified alternative with required month/year and root header/rows. Its rows add `order.posting_number`; oversized responses can return 400 with `/v1/report/realization/posting/create` as the documented alternative. This posting and asynchronous file path is not implemented. Neither verified schema supplies an immutable report UUID/correction version or a complete correction protocol.

## Implemented native draft import

A separate owner consent binds a `CommissionPolicy` to the exact tenant/account pair, configuration revision and policy revision. It is independent of mirror and physical-movement grants. The owner selects the actual commission contract, organization, commissioner, currency, positive exchange rate, price basis, VAT flags/rate and `ZERO_ONLY` treatment of unsupported components. Metadata returns bounded native contracts; a truncated list is explicit. Exact reference reads verify account ownership, the contract's organization/agent/currency and `Commission`/`None` types. Zero or one native bank account can be resolved deterministically; multiple accounts require an explicit account ID. Sensitive bank details are never stored.

The public workflow is `GET /api/orders/finance/metadata`, `POST /api/orders/finance/preview`, `POST /api/orders/finance/approve` and `GET /api/orders/finance/operations`. Existing merchant authentication supplies tenant and owner identity. The preview freezes the full sanitized source, exact canonical product mappings, native reference evidence, accountant choices, configuration and credential revisions, decimal totals, a fingerprint, and a 15-minute approval deadline. Product/variant identity comes from confirmed scoped mappings, never name matching.

The supported subset is a closed monthly report from May 2024 onward with at most 1,000 native sales lines. It requires explicit zero return quantity/amount/reward/loyalty totals and zero bank co-investment/stars; missing values or nonzero legacy components block. A zero-quantity monetary adjustment blocks. Unknown source fields already fail schema validation. `SELLER_PRICE` selects `seller_price_per_instance`; `REALIZED_PRICE` selects `delivery.price_per_instance`. The owner must review that choice. STYK additionally requires selected price × quantity to equal `delivery.amount`, and gross minus signed reward to equal `delivery.total`, exactly in supported currency minor units. These are restrictive STYK consistency gates; the terse Ozon descriptions do not prove a universal formula for `amount` or `total`. Inconsistent reports require accounting review. Automatic rounding and inferred exchange/VAT rates are absent. VAT-exclusive enabled taxation is unsupported and blocks before POST; VAT included or disabled is explicit.

The existing native operation budget also limits each preflight to 128 read
requests. Repeated rows for the same assortment share one item check; many
distinct items or account-reference reads can exhaust that budget before the
1,000-line limit. Such a report is blocked before POST, never partially imported.
This is an STYK bound, not a provider-advertised document limit.

The adapter creates a real [MoySklad `commissionreportin`](https://dev.moysklad.ru/doc/api/remap/1.2/#/documents/commissionreportin) with `applicable=false`, `rewardType=None`, explicit signed position rewards, currency/rate, period, date, contract and exact assortment references. It does not post the document, create a payment, change stock, import report return positions or modify an existing report. Native schema was checked against the official documentation source at `fb6d231b292de82379575e3176dd94347e36cf0c`. `syncId` is read-only for this entity and is never sent. New mirrored customerorders receive the selected commission contract only when the finance policy matches their organization/agent. Existing orders are not overwritten; physical documents copy the actual parent's contract and block a configured mismatch for explicit review.

Migration 73 adds source observations, expiring previews and a single scoped source-period work record, using the existing operations outbox and worker leases. Approval and final dispatch are guarded by current ownership, consent, exact configuration/credentials and mappings. The worker rereads the authoritative source fingerprint and native references before dispatch. The adapter verifies account/reference/product evidence and exact marker absence again before one POST. An exact random `externalCode` is bound to that durable work; it is not treated as a provider uniqueness constraint. After the fifth day, opt-in scheduling enrolls the previous closed month through the same preview/approval/worker path.

Acknowledgement is followed by bounded native header/positions/header readback with exact draft status, references, date/period/rate/VAT, product multiset, quantities, prices, signed rewards, gross and commissioner net. A lost acknowledgement remains UNKNOWN and permits reads only. Restart or lease recovery cannot issue another POST. A changed source fingerprint is recorded and requires manual correction review; no automatic PUT or replacement report is issued. A foreign/preexisting marker is a conflict, not an adopted successful operation.

There is no cross-provider transaction or conditional report-create guard. A merchant can change a reference or create a separate manual report after preflight; local scoped period fencing protects STYK workers, not unrelated external writers. The accountant must reconcile manual documents and source corrections. Provider publication/schema/permission limitations are distinct from unimplemented posting-report, return/loyalty/service/VAT-exclusive mappings and correction workflows.

## Verification and first-customer use

Synthetic HTTP tests assert the real endpoint/body/authentication shape, large identifiers, exact high-value decimals, negative fees, missing/null/zero, privacy removal, correction fingerprints, malformed/schema-drift failures, and one-attempt 403/404/429/503 handling. Service tests cover foreign bindings, disabled source reads and revocation during a provider read. Native adapter tests also exercise signed rewards, real one-POST/readback shape, lost acknowledgements, exact-marker conflicts, revoked admission, and native edits. The later [2026-11-07 LIVE acceptance](live-orders-acceptance-2026-11-07/executive-summary.md) observed seven real monthly sources and the live field extension below. Native accounting writes remain unobserved on those sources because the actual data/reference gates block them.

For an authorized seller, enable source reads, choose a closed month, compare the source with the seller's official document, select and review accountant choices, preview the exact native draft, then approve that preview. Keep monthly scheduling disabled until that first native draft is checked in MoySklad. Report availability is independent of the order's `delivered` status. No amount or stock effect should be inferred from a preview blocker.
## Live source extension reviewed on 2026-11-07

Real monthly report reads returned the numeric field
`delivery_commission.pick_up_point_coinvestment`. The published
[base OpenAPI](https://docs.ozon.ru/api/seller/zh/swagger.json) components
`RowItemCommission` and `RowItemCommissionReturn` do not declare that field.
Its accounting meaning remains unverified. STYK retains its exact decimal and
presence in sanitized source evidence; it does not infer a monetary allocation
from the name. The reproduction under
`internal/infrastructure/ozon/testdata/realization_live_extension_20261107` uses
synthetic identities and amounts, with separately recorded live shape provenance.

The `ZERO_ONLY` draft policy requires an explicit known zero for this component
on both sale and return data. Missing, null, malformed, positive or negative
values block a new draft. An observed `return_commission: null` remains unknown
return evidence; it is never silently converted to zero returns. Other unknown
financial fields still fail closed.

Absent extension fields are omitted from serialization to preserve pre-extension
immutable preview and source hashes. Newly observed null, zero or numeric values
participate in the source fingerprint. Dispatch rechecks current component
support even for older unsent work. Existing dispatched UNKNOWN work remains
eligible for read-only exact document reconciliation; it is never reposted.
