# Ozon fulfillment readback and carriage boundary

Verified on 2026-11-06 using the current [official published OpenAPI](https://docs.ozon.ru/api/seller/zh/swagger.json) and the [official status FAQ](https://dev.ozon.ru/start/257-FAQ-spravochnik-oshibok-i-sposoby-ikh-resheniia). The raw OpenAPI could be read through the web reader; direct TLS download failed, so no raw schema checksum is asserted.

## Confirmed observations after an attempted action

`POST /v4/posting/fbs/ship` accepts `posting_number` and packages containing products (`product_id` is the Ozon SKU, plus integer quantity). Its response `result` lists provider-returned posting numbers. Country and exemplar prerequisites remain separate verified operations. HTTP 200 is explicitly insufficient: read `/v3/posting/fbs/get`; `substatus=ship_failed` means assembly failed.

The executable readback predicate now distinguishes these profiles:

| Attempt | Bound profile | Accepted observed result or successor |
| --- | --- | --- |
| Pack | Existing exact outcome | `awaiting_deliver`, unless a documented failure substatus is present |
| Pack | Verified FBS warehouse/method | `acceptance_in_progress`, `delivering`, `driver_pickup`, `delivered`; exact one-package SKU/offer quantities must still match |
| Pack | Verified non-Express rFBS non-integrated | `delivering`, `driver_pickup`, `delivered`; exact package contents and matching integration required |
| Pack | Verified non-Express rFBS aggregator | `awaiting_registration`, `delivering`, `driver_pickup`, `delivered`; exact package contents and matching aggregator integration required |
| Seller delivering | Verified non-Express rFBS non-integrated | `delivering`, or observed `driver_pickup` / `delivered` |
| Seller last-mile | Verified non-Express rFBS non-integrated | `driver_pickup`, or observed `delivered` |
| Seller delivered | Existing exact outcome | `delivered` |
| Cancellation | Existing exact outcome | `cancelled`; delivery states are not cancellation success |

This is an explicit allowlist for confirming an already attempted command, not a general ordering of statuses. Unverified hybrid, Express, unknown carrier, altered warehouse/method/scheme, conflicting integration, changed package quantity, registration error, cancellation, arbitration and unknown states do not borrow a successor rule. Label, country, exemplars, tracking, partial cancellation and split keep their separate resource/content readbacks. Existing exact expected outcomes retain compatibility.

There is a discrepancy inside the published Swagger: the generic ship description says `awaiting_deliver`, while its aggregator process says `awaiting_registration` and then `awaiting_delivery`. The aggregator-specific registration stage is accepted only with exact aggregator evidence. The `awaiting_delivery` spelling does not appear in the canonical status list; STYK does not silently alias it to `awaiting_deliver`.

The provider FAQ and process explain carrier-owned delivery states for integrated/aggregator routes and the delivering → last-mile → delivered steps for non-integrated delivery. The change does not grant a seller delivery API to an aggregator or infer a command from any incoming status. `acceptance_in_progress` can confirm that packing occurred but is not completed Ozon acceptance. Packing/registration do not establish physical handoff. A delivered observation is not payment evidence.

Runner and PostgreSQL completion validation use the same predicate. Successful confirmation records the observed posting and completes the existing durable work item; it performs no native PUT, demand, return or additional Ozon write. Matching the desired observed result does not establish which actor caused it. UNKNOWN requests remain readback-only, without blind resubmission.

## Why carriage is a separate account batch workflow

[Official migration notice 789](https://dev.ozon.ru/news/789-Izmeneniia-v-metodakh-otgruzki-Seller-API/) names `/v1/carriage/create` and `/v1/carriage/approve` as the replacements for the older act-create method. The current OpenAPI verifies:

| Operation | Request | Response / state |
| --- | --- | --- |
| `POST /v1/carriage/create` | `delivery_method_id` integer int64, optional `departure_date` date-time, `all_blr_traceable` boolean; the schema itself does not declare a required list | `carriage_id` integer int64; carriage starts `new` |
| `POST /v1/carriage/approve` | Required `carriage_id`; conditional `containers_count` int32 only when trust acceptance by cargo places is enabled | HTTP 200 has no JSON response body; carriage becomes formed |
| `POST /v1/carriage/set-postings` | Required `carriage_id`, `posting_numbers` | Per-posting result/error information; changes the composition of a `new` carriage |
| `POST /v2/posting/fbs/act/get-postings` | Required `id` (the carriage identity) | Exact member postings and products |
| `POST /v1/carriage/get` | Required `carriage_id` | Carriage state and available actions |

Create selects **all ready postings** for the delivery method; it does not accept the single order's `posting_number`. Using it as an incidental per-order action could include other live orders and race with another employee's batch. Approve means formed, not physical transfer or completed acceptance. Neither checked request defines an idempotency token.

A complete carriage writer therefore needs an account/method batch intent, explicit membership/evidence review and consent, durable receipt/UNKNOWN fencing, exact carriage/member readback, and unknown-create reconciliation before any retry. That intent can extend the existing operations/outbox/runner; it does not require a competing orchestrator. The new reverse assembly path issues `/ship` only; it does not represent itself as creating or approving a carriage. Carriage writes remain an implementation gap in this change, not an API prohibition.

## Final completeness audit, 2026-11-07 (2026-11-06 UTC)

Rechecked the operation and component schemas in the same official OpenAPI, including `v1CarriageCreateRequest`, `v1CarriageApproveRequest`, `carriageCarriageGetRequest`, `carriageCarriageGetResponse`, `v1SetPostingsRequest`, `v1SetPostingsResponse`, `v2PostingFBSActGetPostingsRequest` and `v2PostingFBSActGetPostingsResponse`.

The known-ID read path is available. `carriage/get` returns identity, method, warehouse, creation/update times, status, actions and containers. `act/get-postings` returns a `result` array with posting identity, status, error and product SKU/offer/quantity data. Neither checked request has a revision precondition. The checked `get.status` schema is a string without a status enum; “formed” in the approve description must not become a guessed machine value. Missing/null members or status cannot prove success.

### Discovery contract verification gap

[Official notice 648](https://dev.ozon.ru/news/648-Izmeneniia-v-metodakh-Seller-API/) replaces both `/v1/carriage/delivery/list` and `/v1/posting/carriage-available/list` with `/v2/carriage/delivery/list`, effective 2026-03-20. These older methods must not be used for a new implementation.

The Chinese OpenAPI used above does not expose the replacement operation or its components. The [main official Swagger](https://docs.ozon.ru/api/seller/swagger.json) exceeds the web reader's 4 MiB limit (`Content length is too large: 4194305+`). Direct unauthenticated documentation downloads returned proxy failures or HTTP 403 after a site redirect; no browser connector was available. A subsequent attempt with the project's Playwright HTTP client and a fresh Chromium context also returned HTTP 403 `Antibot Challenge Page` for both the main and `/en/swagger.json` URLs. No challenge was solved and no third-party contract was accepted as authority. The public operation page and official notices did not supply a smaller current schema. Consequently this audit does **not** assert the current discovery request/response fields, pagination guarantees, completeness, or canonical post-approval statuses. This is a verification limitation of this session, not proof that Ozon lacks discovery. A third-party schema mirror is not substituted for the current primary contract.

### Minimal reuse of STYK's existing execution path

There is no verified provider-contract prohibition on a carriage writer. A bounded implementation can reuse existing operations, durable work, dispatch evidence, provider admission, UNKNOWN readback and worker entry points, with these explicit extensions:

1. Add a typed carriage target and actions to the existing command/work model. Current commands require a real posting, posting correlation and warehouse mapping; a synthetic posting number must not impersonate an account batch. Persist reviewed method/date/traceability/container inputs and member fingerprints in scoped batch evidence. Use a migration for the new action constraints and batch evidence, preserving existing order commands.
2. Add a durable fence for the **Ozon provider account and method**, shared across account-pair configurations. A fence scoped only to a МойСклад/Ozon pair would allow two pairs using the same Ozon account to create competing batches. Use the existing worker/admission flow; never keep a database transaction open across HTTP.
3. Require explicit authority for **all ready postings at dispatch time** for that account/method. A selected order's assembly/handoff consent does not authorize this expansion. New ready orders can enter between preview and create; exact preview membership is therefore insufficient authority for create. After receiving and durably saving `carriage_id`, read the actual header and all members and stop before approve if any member lacks the necessary authority or evidence. Do not automatically trim, cancel or move another employee's members.
4. Treat approve as a separate durable command with known carriage ID and reviewed exact composition/container evidence. Repeat header/member checks immediately before admission and read them again afterward. `set-postings` is an explicit composition replacement with per-posting outcomes, not an atomic compare-and-swap; partial outcomes need fresh membership reads. A changed or unknown composition blocks approval. Even these reads cannot close an external employee's last-moment edit race without provider support or an operational ownership agreement.
5. When create's response is lost, persist UNKNOWN and never resend it automatically. Discovery may supply candidates, but matching account/method/date does not identify the actor or original request. Only an unambiguous candidate with exact authorized members can be proposed for explicit adoption; absent or multiple candidates remain unresolved. Define candidate enumeration and any automatic outcome-based reconciliation only after verifying the current discovery schema and visibility guarantees. Unknown approve is likewise read-only by its already known ID; acceptance/physical handoff must not be inferred from document formation.

The required acceptance tests follow the existing HTTP-adapter/PostgreSQL/outbox fixtures: successful create/known-ID readback/approve; timeout before receiving ID; concurrent batch approval; changed membership after preview; unknown/null list fields; partial composition results; owner revocation after provider admission; cross-pair contention on the same provider method; lease expiry and replay without a duplicate write. None of these carriage paths is implemented or tested in this change. Production-provider calls were not made for this audit.
