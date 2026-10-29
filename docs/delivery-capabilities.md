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
`/v2/delivery-method/list` with `{filter:{},limit:50,offset:0}`. These structures
were observed in the bounded read reported in [orders-live-evidence.md](orders-live-evidence.md).
Warehouse traversal is bounded to ten pages; method continuation is rejected
until its current semantics are reviewed. A response cursor does not prove that
a request accepts one. The five-minute cache belongs to one exact scoped client;
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

Migration 47 persists tenant/account/warehouse/method/revision observations with
exact contract hash, contract check date, observation/check times and expiry.
Fact revisions remain stable across freshness checks. Empty method identity is
an explicit warehouse-only observation. Writes are transactional; reads reject
old contracts and expired evidence. These observations do not remove the existing
rFBS stock guards or claim stock publication readiness. Rollback refuses to
discard observation history while the table is populated; no provider operation
is issued.

The pure capability matrix reports implementation, official request-contract
verification, deterministic testing and live observation independently, alongside
the registry's workflow prerequisites. A passing fixture is not live evidence.
