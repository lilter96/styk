# Delivery capability evidence

`internal/delivery` models scheme, warehouse status, warehouse type, Express,
carrier integration, destination and delivery-method identity independently.
Warehouse names, endpoint spelling, posting status and numeric equality between
a warehouse ID and method ID establish none of the other dimensions. FBO remains
excluded. Unknown carrier/destination and absent Express values stay unknown.

The Ozon order reader can opt into `OrderReadOptions.ResolveDelivery` and bind
`DeliveryObserver` to the PostgreSQL store. Fresh v3 posting details need not
contain a scheme: a matching explicit warehouse type supplies FBS/rFBS evidence.
`created` and `disabled` are observed warehouse states, not active-publication
permissions. FBS methods absent from the method list are not fabricated.

Discovery uses `/v2/warehouse/list` with `{limit:100,cursor:""}` and
`/v2/delivery-method/list` with cursor, limit 100, `sort_dir: "ASC"` and an explicit
filter containing all six documented lifecycle statuses. The v2 method contract
was checked against the [official OpenAPI](https://docs.ozon.ru/api/seller/zh/swagger.json)
on 2026-11-04 (`v2DeliveryMethodListV2Request`,
`v2DeliveryMethodListV2Response`). The earlier offset request was a legacy-contract
defect, not a provider pagination limitation. Both traversals are bounded to ten
pages/1,000 entities. Missing/repeated continuation cursors, duplicate identities
and partial responses are rejected; partial results are not cached as complete. The five-minute cache belongs to one exact scoped client;
concurrent readers share one bounded discovery and cannot mutate its cache.

[Official notice 648](https://dev.ozon.ru/news/648-Izmeneniia-v-metodakh-Seller-API/)
confirms the current warehouse/method endpoint versions.
[The Express article](https://dev.ozon.ru/start/449-RFBS-Express-Rabota-so-skladami-i-metodami-dostavki-cherez-Seller-API/)
distinguishes aggregator and nonintegrated warehouses. Neither verifies a full
request schema or posting transition authority.
[Notice 638](https://dev.ozon.ru/news/638-Novyi-protsess-dostavki-zakazov-v-punkty-vydachi-PVZ/)
describes independent posting-level hybrid/PVZ conditions; warehouse type alone
does not establish those conditions. The complete official Seller API schema
remains unverified in the executable order manifest.

The 2026-11-05 review completely verified only the selected new-label request and
HTTP-200 response schemas, not the complete Seller API or every delivery profile.
The [graded corpus provenance](../internal/ordercontract/testdata/curated_20261105/provenance.json)
separates official schemas/examples, generated failure cases and third-party
normalized status shapes. In particular, heuristic FBS/rFBS labels in a public
normalized corpus do not establish warehouse or action capabilities.

Migration 47 persists tenant/account/warehouse/method/revision observations with
exact contract hash, contract check date, observation/check times and expiry.
Fact revisions remain stable across freshness checks. Empty method identity is
an explicit warehouse-only observation. Writes are transactional; reads reject
old contracts and expired evidence. Method lifecycle status is preserved independently, including missing/null and
unknown future values. A disabled method is not automatically evidence that an
already assigned posting cannot be fulfilled: each action still needs its own
validated prerequisites. Legacy profile identities without this new observation
remain valid. These observations do not enroll targets or grant stock publication
permission; the exact-account test policy and reviewed mappings remain required. Rollback refuses to
discard observation history while the table is populated; no provider operation
is issued.

The pure capability matrix reports implementation, official request-contract
verification, deterministic testing and live observation independently, alongside
the registry's workflow prerequisites. A passing fixture is not live evidence.
