# Orders: bounded live discovery

Read-only phase completed **2026-10-29 22:14 UTC** (2026-10-30 in Europe/Minsk).
Scope: the existing `cross-e2e` pair, independently matched to the private
manifest and enabled encrypted PostgreSQL credentials. Account-ID SHA-256
prefixes: MoySklad `8fd183ebcacb`, Ozon `a3a518ffbc98`.

**Observed:** two genuine historical FBS postings, both `cancelled` /
`posting_canceled`, each with one product. The v4 list covered
2026-09-29T22:12:34Z–2026-10-29T22:12:34Z and returned `has_next=false`.
Both exact v3 get requests succeeded. Neither posting contains the selected
`ATEST-897677` target. Both product lines already exist in the latest local
Ozon import and have confirmed product mappings. The planned longer search
stopped after this first 30-day interval; no year-wide traversal is claimed.

The v4 unfulfilled query for cutoff dates
2026-10-14T22:12:40Z–2026-11-13T22:12:40Z returned no postings and
`has_next=false`. Actionable FBS/rFBS orders and live packing, labels, tracking,
shipment or delivery transitions are **NOT_OBSERVED_LIVE**. A later historical
mirror must use `applicable=false` and position reserve zero; this phase created
no mirror and changed no provider object. No purchase was fabricated.

Fresh known-target reads matched product `2764544165`, offer `ATEST-897677`,
SKU `2889277907` and FBS warehouse `1020001323428000`. Its exact stock row was
present/reserved/free **0/0/0**. The complete warehouse page contained 13 rows:
7 FBS, 3 ordinary rFBS and 3 rFBS Express. Five reported `created`, eight
`disabled`. The delivery-method page contained 10 distinct method IDs and
`has_next=false`. Warehouse and delivery-method identities remain separate;
the observed FBS posting method ID happens to equal its warehouse ID. Neither
endpoint spelling nor a warehouse name establishes scheme or transition rights.

Bounded native MoySklad discovery found one organization, three stores, the
existing synthetic counterparty by exact fixture ID, one currency, one sales
channel, seven customerorder states and zero custom attributes. Names, contact
details, addresses and customer payload values are excluded from this report.

| Provider | Read attempts | Writes | Original total budget | Recovery reserve within total |
|---|---:|---:|---|---|
| Ozon | 11 | 0 | 120 reads / 20 writes | 20 reads / 5 writes |
| MoySklad | 8 | 0 | 100 reads / 25 writes | 20 reads / 5 writes |

Eighteen attempts returned HTTP 200. One initial product query with mixed
identity filters returned HTTP 400; a separately journaled offer-only query
succeeded. There were no blind retries or 429 responses. Every attempt had a
durable local intent before dispatch, one attempt, a 15-second absolute deadline,
4 MiB compressed and decoded response bounds, redirect rejection and at least
one second between requests to the same provider. Retry-After handling and a
provider rate-limit hold are implemented in the private read-only diagnostic.
The forbidden logistics-information endpoint was not called.

Before and after discovery, PostgreSQL remained on migration **44**, with stock
publication, automatic stock sync and catalog sync disabled, no pending/running
outbox task and no orders-configuration table. Migrations 45/46 were not applied.
The credential key, v6 helper and v6 journal SHA-256 values remained unchanged.

The executable [contract manifest](order-contract-manifest.json) was read;
its byte SHA-256 was
`10a3380688dd9f639ef0c3a4883ec3c481c102a29e5f1fe43446ff70c499a572`.
On the observation date, official Ozon notices verified current
[v4 posting versions](https://dev.ozon.ru/news/734-Otkliuchenie-starykh-metodov-Seller-API-dlia-raboty-s-postingami/),
[v2 warehouse/delivery versions](https://dev.ozon.ru/news/648-Izmeneniia-v-metodakh-Seller-API/)
and [new label versions/scanit](https://dev.ozon.ru/news/800-Podderzhka-novoi-etiketki-scanit-i-migratsiia-metodov-v-Seller-API/).
These were readable through the web reader; direct downloads returned 307 and
were not followed. Their manifest hashes cover evidence summaries, not downloaded
HTML. The [full Ozon schema](https://docs.ozon.ru/api/seller/) remained unreadable.
Successful requests and captured structural signatures are observed behavior,
not verification of a complete official schema or all workflow prerequisites.

The official MoySklad
[customerorder source](https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/documents/_customerOrder.md)
returned HTTP 200 with byte SHA-256
`990e50b153bd64ad0b423710f88fc435564878aa95485b792759415f0563c972`.
The general, organization, counterparty, store, currency and saleschannel source
downloads also returned HTTP 200 and matched the byte hashes already recorded
in the manifest. This confirms those downloaded sources; it does not make the
live dictionary responses conform to a complete reviewed allowlist.

Private, ignored handoff files are `.local/orders-live/journal.json`,
`.local/orders-live/mirror-inputs.json` and
`.local/orders-live/source-evidence.json`. They contain safe identifiers,
value-free structural signatures, request outcomes, configuration references
and source hashes. Two targeted refresh payloads are retained only as
`response-*.encrypted.json`, encrypted using the unchanged local key; raw
customer data and credentials were never written. Existing v6 evidence was
preserved. No application mutation dispatcher or provider publisher was added.

Shared-tree `go test -race ./...`, `make test`, `go build ./...` and `make build`
passed during this phase. The initial `make lint` encountered an unrelated
ignored-file formatting issue; final integrated quality checks belong to the
release phase. No live write or production-readiness conclusion follows from
these read-only observations.
