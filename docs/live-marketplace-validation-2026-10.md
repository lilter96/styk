# Live marketplace read validation — 2026-10-29

## Scope and outcome

On 2026-10-29, 10:53:52–10:55:27 UTC, the configured, owner-authorized local Ozon and MoySklad accounts were queried over HTTPS through STYK's existing read clients. This is **actual live evidence**, separate from the repository's synthetic fixtures. It is a bounded observation of these two accounts, not a proof of a consistent or exhaustive inventory snapshot, shared ownership, or publication readiness.

Both environment credentials matched the enabled encrypted credentials for the exact registered accounts. The local MoySklad employee-context account identity matched the configured account; this establishes access to that account, not legal or business ownership. Both accounts were registered in the same local workspace; registration is an operator assertion, not evidence that their inventory is shared. No credentials, account identifiers, customer data, request bodies or raw provider responses are included in this report.

A temporary, independently tested transport guard accepted only the exact official HTTPS hosts and enumerated read methods. It denied writes and redirects, used fresh connections and one attempt per logical request, rejected responses at the 4 MiB boundary, capped pages at 100 entities, applied a maximum 15-second request timeout and an interval of at least one second, and stopped on authorization/rate-limit responses. Provider-client limits also bounded the in-memory catalog traversals. No raw provider response was persisted. No background importer, publication worker or external write was started; catalog traversals were not saved as new imports. Customer order lists used a 30-day window, no optional Ozon analytics/financial/legal/barcode expansions, and a maximum of 10 MoySklad orders and 10 positions from one order.

| Provider | Logical requests | HTTP attempts | HTTP 2xx | HTTP 4xx/5xx | Transport failures |
|---|---:|---:|---:|---:|---:|
| Ozon | 6 | 6 | 6 | 0 | 0 |
| MoySklad | 8 | 8 | 8 | 0 | 0 |

All responses were HTTP 200. No retry, 401/403/429 or `Retry-After` was observed. The largest body was the Ozon warehouse response, 325,773 bytes. The additional Ozon posting-list call is included in the six requests; it repeated the bounded list solely to record sanitized status/line statistics that were not captured in the first pass. It was not an automatic retry.

## Ozon

All Ozon calls used POST as the provider's **read-only** request convention, with no write endpoint in the transport allowlist:

| Endpoint | Requests | Outcome and scope |
|---|---:|---|
| `/v3/product/list` | 2 | HTTP 200, one page each for `ALL` and `ARCHIVED` |
| `/v2/warehouse/list` | 1 | HTTP 200, one page |
| `/v2/product/info/stocks-by-warehouse/fbs` | 1 | HTTP 200, one page for the observed offer IDs |
| `/v4/posting/fbs/list` | 2 | HTTP 200, the same bounded 30-day list sampled twice |

The catalog traversal completed through the existing Go `ReadBounded` parser: **33 products**, of which 9 were non-archived and 24 archived. Product and offer identifiers were present in all 33. Product-list `sku` was numeric and explicitly **0 in five records** (three non-archived, two archived); all 28 others were positive. The client deliberately normalizes a non-positive SKU to unavailable/NULL, rather than treating zero as a valid SKU. `archived` was present for all items. Both product-list responses supplied `total_items` and `last_id`. The result totals matched the returned records. The sample fit on one page per visibility; continuation pagination was **not live-tested**.

There were **13 warehouses** with identifiers, names, status strings and explicit `is_rfbs` flags: seven FBS (`false`) and six rFBS (`true`). The observed status values were five `created` and eight `disabled`: one FBS and four rFBS were `created`; six FBS and two rFBS were `disabled`. These are returned status values, not an independently verified definition of operational availability. The warehouse page returned `has_next=false`.

The stock endpoint returned **five product–warehouse rows for four distinct products**; the other 29 catalog products had no row in this traversal and must not be interpreted as zero. All five rows explicitly reported numeric `present=0`, `reserved=0`, and `free_stock=0`, with product, offer, positive SKU and warehouse identifiers present. All five stock SKUs matched the product-list SKU. Three rows joined warehouses marked FBS and **two joined warehouses marked rFBS**, despite the endpoint's `/fbs` suffix. Three rows belonged to non-archived products and two to archived products; one row joined a warehouse with status `disabled`. Neither `updated_at` nor `observed_at` appeared in these rows. The stock page returned `has_next=false`.

This mixed warehouse result is an important interpretation constraint: the current importer preserves all returned rows in the table named `ozon_fbs_stock_observations`; it must not be assumed that every row in that table belongs to an FBS warehouse. Warehouse classification is separately observed, and existing warehouse mapping and publication review require `is_rfbs=false`. The current publication review does not establish warehouse availability from the returned `status` value. The real-write hold remains in effect, and status semantics need provider-contract review before any future write consideration. No filtering or business-model change was made based solely on the endpoint name.

The 30-day FBS posting-list sample returned **two postings**, both with status `cancelled`, and two product lines in total. Posting number, order ID, status/substatus, processing/shipment timestamp fields, warehouse ID, offer, SKU and positive line quantity were present in the sampled list. `has_next=false`; the top-level list had no outer `result`. No posting-detail or customer-information expansion was requested. These two records do not establish coverage of all current outstanding obligations or their overlap with source reserves. The exact list DTO was exercised; the full posting-detail traversal was not.

The observed v2 warehouse/stock paths are consistent with Ozon's [announced replacements](https://dev.ozon.ru/news/648-Izmeneniia-v-metodakh-Seller-API/). Ozon [announced `total_items` in place of `total`](https://dev.ozon.ru/news/801-Otkaz-ot-polia-total-v-metodakh-Seller-API/) and lists `/v4/posting/fbs/list` among its [current posting methods](https://dev.ozon.ru/news/734-Otkliuchenie-starykh-metodov-Seller-API-dlia-raboty-s-postingami/). These notices and this sample do not prove the entire current schema or the behavior of the write API.

## MoySklad

All MoySklad calls were GET:

| Endpoint | Requests | Outcome and scope |
|---|---:|---|
| `/api/remap/1.2/context/employee` | 1 | HTTP 200, account identity check |
| `/api/remap/1.2/entity/product` | 1 | HTTP 200, one page |
| `/api/remap/1.2/entity/variant` | 1 | HTTP 200, one page |
| `/api/remap/1.2/entity/store` | 1 | HTTP 200, one page |
| `/api/remap/1.2/report/stock/all` | 1 | HTTP 200, one page |
| `/api/remap/1.2/report/stock/bystore` | 1 | HTTP 200, one page |
| `/api/remap/1.2/entity/customerorder` | 1 | HTTP 200, 30-day bounded list, limit 10 |
| `/api/remap/1.2/entity/customerorder/{id}/positions` | 1 | HTTP 200, one order's positions, limit 10 |

The existing Go `ReadBoundedProgress` parser completed a five-page catalog/stock traversal: **nine products, two variants and three warehouses**. Both variants had a parent reference matching a product in the observed catalog. Product/variant identifiers, account IDs, codes and source `updated` fields were present in the sampled records; one of the nine products omitted `article`. The current model turns an omitted article into an empty string and therefore does not distinguish absent from explicitly empty. The three warehouse records included `archived=false` and source `updated`; the existing generic entity model does not retain that archive flag. No separate warehouse availability/status semantics were established. Each collection fit within a single page, so multi-page behavior was not validated live.

The aggregate stock report contained **11 rows**: six explicit zero physical stocks, eight explicit zero reserves and three positive reserves; all 11 incoming values were zero. `quantity` was present for all 11 aggregate rows; `freeStock` was absent in all 11. The warehouse report contained **33 item–warehouse rows**: 27 explicit zero physical stocks, 30 explicit zero reserves, three positive reserves and 33 zero incoming quantities. Both `quantity` and `freeStock` were absent in all 33 warehouse rows. No warehouse availability was derived. Source `updated` was not present in the examined stock row/store fields. Product and warehouse entity `updated` timestamps are not a stock snapshot watermark.

The original completed batch and the live traversal had identical fingerprints. In the persisted snapshot corresponding to the live content, all 11 aggregate physical values and all 11 aggregate reserve values numerically matched the sums of their warehouse rows. This is an observed equality in this sample, **not** evidence of atomicity, complete visibility, reserve attribution or general source semantics. MoySklad's [stock report documentation](https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/reports/_report_stock.md) notes permission-dependent visibility and non-instantaneous stock recalculation.

The 30-day customer-order list contained **five orders**; all five included account ID, source update timestamp, store, state and `applicable` fields. Only one order's positions were requested: two positions, both with IDs, assortment references, numeric quantity, reserve and shipped fields; both sampled position reserves and shipped values were zero, and neither supplied a pack object. The list and position records decoded with the existing Go DTOs. No order-detail endpoint or full customer-order traversal was run, and no customer names, contact details or raw bodies were retained. The sample is insufficient to attribute all source reserve or match order obligations to Ozon.

## Cross-system identity and existing history

The two current in-memory traversals produced **zero exact product-ID pairs and zero exact warehouse-ID pairs**. Exact comparisons of Ozon offer IDs and SKUs against MoySklad product/variant articles and codes also found **zero candidate pairs**; a normalized offer/article comparison likewise found zero, as did a normalized warehouse-name comparison. Names, barcodes, pack composition and other potential evidence were not comprehensively compared. A matching string, even if found later, would be a candidate, not proof of identity.

The local database already contained **one scoped product mapping** for these accounts. Both referenced products and the mapped Ozon offer were present in the latest observations, but the Ozon product was **archived**. There were **no active scoped warehouse mappings**. The existing product mapping, same-workspace registration and coincident quantities do not independently prove shared inventory, units or ownership. This sample cannot establish that the accounts are related, nor prove that they are unrelated. No mapping was added, changed or confirmed.

The exact-account prior completed batches were read without attaching them to another tenant or modifying them. The previous MoySklad batch started at 09:52:19 UTC and completed at 09:52:21 UTC; all 14 entity IDs (products, variants and stores) and all 33 warehouse-stock targets remained present, and all 33 physical/reserve/incoming triples matched. The previous Ozon batch started at 09:52:26 UTC and completed at 09:52:27 UTC; all 33 product IDs, 13 warehouse IDs and five stock targets remained present, and all five stock triples matched. Both current complete traversals produced the **same existing importer fingerprint** as their latest stored batch, so the existing deduplication logic would not create a fresh identical batch. No new batch was written. Fingerprint equality does not make the old batch's observation time current or prove that future reads will remain unchanged.

## Publication-safety evidence

The classifications below describe what this sample establishes, not what a provider might make available through other calls.

| Condition | Classification | Evidence and limit |
|---|---|---|
| Source physical stock | AVAILABLE_FROM_MOYSKLAD | 33 warehouse rows, including explicit zeros; missing rows remain unknown and the traversal is not a consistent snapshot. |
| Source reserve | AVAILABLE_FROM_MOYSKLAD | Reserve was present on all 33 sampled warehouse rows; attribution and unassigned reserves are not proved. |
| Outstanding orders | DERIVABLE_WITH_LIMITATIONS | Five MoySklad orders and two cancelled Ozon postings in a bounded 30-day window; one order's positions sampled. Coverage and in-flight obligations are not established. |
| Order-to-reservation correlation | OPERATOR_ATTESTATION_REQUIRED | Line fields exist in the sample, but no independent end-to-end identity, reflection or overlap with the stock baseline was established. |
| Marketplace reserved stock | AVAILABLE_FROM_OZON | Explicit zero on five returned stock rows only; no row for 29 products is not a zero. |
| Unit/pack equivalence | OPERATOR_ATTESTATION_REQUIRED | Product/variant identity and units across systems were not established; the sampled positions did not contain pack objects. |
| Shared inventory and other channels | OPERATOR_ATTESTATION_REQUIRED | No automatic matching or complete account/channel exposure evidence; the existing archived-product mapping is insufficient. |
| Reliable source snapshot ordering | NOT_AVAILABLE | The inspected stock rows did not expose a verified source stock revision or transactional watermark; local batch times and entity `updated` fields do not supply one. |
| Competing stock writers | OPERATOR_ATTESTATION_REQUIRED | No examined read proves exclusivity or settles in-flight writes; an assertion is not independent verification. |
| Product and warehouse identity | OPERATOR_ATTESTATION_REQUIRED | One existing product mapping points to an archived listing; no active warehouse mapping or independently supported cross-system pair. |
| Remote write ordering, idempotency and outcome causality | NOT_AVAILABLE | Read-only observations cannot prove that a future write is fenced, applied once or causally confirmed. |

Before and after validation, the local database contained **zero stock publication operations and zero publication targets**. The read-only database sessions used PostgreSQL's `default_transaction_read_only=on`. The temporary guard made no request to an external stock-write endpoint. The source-level API gate, default-disabled worker and literal-loopback-only Ozon write adapter remain unchanged. No local API or worker process was running; running processes on other hosts were not assessed. A fresh worker binary built from clean commit `5f1bd670b8dc0acf96221c3577a36077ae98f8eb` contained the gate and adapter symbols; it was not started. The existing local `bin/worker` identified itself as an earlier, modified build, so its provenance was not treated as proof of the current source. The [safety audit](stock-publication-safety-audit.md) still applies.

## Verification and next validation

The temporary read-only guard passed synthetic checks for endpoint allowlisting, request-count limits, oversized responses and stopping after authentication errors. Go lint/vet and race-enabled unit tests for both provider clients, credentials, console and stock-sync runner passed. The existing PostgreSQL publication safety suite passed **28 top-level tests** under the race detector against a disposable database and synthetic HTTP endpoints. No production database, provider credential, mapping or completed import was modified.

- **A — Full live read-only background catalog import:** suitable as the next **controlled validation** for these exact accounts, with explicit bounded settings, verified current worker binary and the real-write hold intact. This report did not start such an import. A successful in-memory traversal and matching fingerprint do not establish production readiness.
- **B — Further API compatibility investigation:** still needed for multi-page live traversal, complete order/detail contracts, missing/zero SKU meanings, FBS/rFBS stock interpretation and `created`/`disabled` warehouse status. The live catalog parsers completed without a demonstrated decoding incompatibility, so no production parser change was made.
- **C — Preparation of a single-target write experiment:** **not recommended**. Exact source/offer and warehouse identity, unit equivalence, reservation attribution, competing writers, remote fencing and unknown-outcome settlement remain unresolved. Read-only success is not authorization or evidence of safe external writes.
