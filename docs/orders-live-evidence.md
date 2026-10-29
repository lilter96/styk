# Controlled live orders and stock evidence

**SUCCESS for two historical order mirrors and the bounded FBS stock regression.**
Final verification: **2026-10-29 23:09:54 UTC** / 2026-10-30 Europe/Minsk.
The registered `cross-e2e` pair matched the private manifest, enabled encrypted
credentials and authenticated provider evidence. Account-ID SHA-256 prefixes:
MoySklad `8fd183ebcacb`, Ozon `a3a518ffbc98`. These are real test accounts;
buyer isolation and general production readiness are not claimed.

## Genuine postings and native mirrors

The v4 list for 2026-09-29T22:12:34Z–2026-10-29T22:12:34Z returned two genuine
FBS postings, both `cancelled` / `posting_canceled`, with `has_next=false`.
Exact v3 reads confirmed each one-product posting. Neither contained the selected
stock-regression offer. Both product lines were already imported and mapped;
no purchase, posting or product correspondence was fabricated. The longer planned
search stopped after this 30-day interval. A bounded v4 unfulfilled query returned
no postings; this does not establish exhaustive coverage or exclude future orders.

Normal STYK API configuration, scheduler ingestion, explicit API previews and
approvals, durable PostgreSQL intents/outbox, and concurrent normal workers created
and reconciled these native customerorders:

| Local operation | Work | Provider HTTP | Dispatches | Final state |
|---|---|---:|---:|---|
| `370bb4a78fa4cbaf8f0804d457ae85ed` | `746a843dd989e5f3c53df1e87a8a845e` | 200 | 1 | RECONCILED |
| `8a93465db0112d787c61f471ff7a7f27` | `e291f79c64af72a4bceb2e4a4bd10a2b` | 200 | 1 | RECONCILED |

Readback verified native organization, synthetic agent, mapped store, cancelled
state, sales channel, currency, complete positions, exact prices and quantities.
Both documents have **`applicable=false` and position reserve `0`**. PII projection
was disabled; unavailable buyer/address data were not invented. Missing source
`created_at` was not mapped to a fabricated moment. Missing native `rate.value`
remains missing; STYK supplied no FX rate. No create was replayed.

Native discovery found one organization, three stores, five counterparties, seven
states and no custom attributes. The account initially had only default BYN;
source lines explicitly specified RUB. One separately authorized native system
RUB currency was created with provider-managed automatic parameters and read back
by its returned ID; BYN remains default. An earlier setup attempt returned HTTP
415. Its rejected intent was retained; a fresh, explicitly authorized gzip request
succeeded. This reference setup is separate from the two normal-pipeline mirrors.

One early accepted order receipt retains a local sanitization code `PROVIDER_ERROR`
despite HTTP 200. Its reconciled state and native evidence are preserved. The
code-normalization fix is present in the second receipt; history was not rewritten.

Desktop 1440px and mobile 390px live Orders screens were inspected: both reconciled
rows and native links appeared, without horizontal overflow or browser errors.
Screenshots are encrypted and native selectors/account identity are masked.
Two normal API processes and two dispatch plus two schedule worker processes ran
against the real pair; shared ingestion and dispatch fences prevented duplicates.

## Bounded stock regression

Only product `2764544165`, offer `ATEST-897677`, SKU `2889277907`, FBS warehouse
`1020001323428000`, mapped to source `e7de650f-d389-11f1-0a80-1ab800275063` in
store `7035238c-d31c-11f1-0a80-071500070d0a`, was published. Existing mappings
`2/v1` and `1/v1` were reused. Fresh normal imports established source 0/0 and
Ozon present/reserved/free 0/0/0 before the experiment.

Exactly one applicable source entry and one later loss moved physical stock
**0 → 1 → 0**, independently read back and verified through normal imports.
The normal calculator produced genuine eligible quantities 1 and 0, using fresh,
explicitly classified `OPERATOR_ATTESTED` test assumptions. Those assumptions
include one-to-one units and assumed zero unseen/unreflected obligations and
competing exposure; they are not independent atomic snapshots or exhaustive
order coverage. The owner accepted possible one-unit buyer exposure.

| Normal publication | Dispatch UTC | Provider result | Matching readback UTC |
|---|---|---|---|
| `ba574bf36a7bba7c30f85a1da6b157fa`, quantity 1 | 23:02:36.459025 | HTTP 200, updated true, no errors | 1 at 23:02:37.841626 |
| `4fe642fcece0483704d54be268ed4969`, quantity 0 | 23:07:01.755126 | HTTP 200, updated true, no errors | 0 at 23:07:03.063091 |

Both reached **RECONCILED**, with one normal-worker dispatch each, **265.296101
seconds apart**. No direct Ozon write harness was used. Before the loss, the first
publication was reconciled, fresh imports showed reserve zero, and current gated
posting discovery found no selected-target posting. No uncertain outcome was
compensated or retried. Final source batch **43** reports physical/reserve **0/0**;
the independent final Ozon batch **44** reports present/reserved/free **0/0/0**.

## Counts, preservation and contract limits

| Provider | Read attempts | Write attempts | Accepted writes | Final aggregate budget |
|---|---:|---:|---:|---|
| MoySklad | **109** | **6** | **5**: currency, two drafts, entry, loss | 140 reads / 25 writes |
| Ozon | **65** | **2** | **2** selected stock updates | 150 reads / 20 writes |

Budgets include recovery reserves of 20 reads / 5 writes per provider. Counts
include the initial read-only phase (MoySklad 8, Ozon 11), five response-less
MoySklad transport attempts, one Ozon HTTP 400 read and the rejected MoySklad
HTTP 415 setup attempt. No provider 429 was observed. A legacy normal order-evidence
refresh made **five MoySklad and one Ozon read absent from the coordinator ledger**;
its returned attempt counters are counted separately here. The legacy coordination
fix was built afterward; no additional live request was used to claim its validation.
All other phase-two attempts were recorded by the shared PostgreSQL gate.

Requests were bounded, single-attempt, redirect-rejecting and paced. New fixture
mutations had exclusive durable local intents; normal mutations had application
intents/outbox records. No forbidden logistics-information endpoint, financial,
payment, shipment, refund or demand document was used. No new write remains UNKNOWN;
the original superseded historical stock UNKNOWN remains unchanged, without an
active UNKNOWN fence.

Migrations 45–47 and then index-only 48 were applied after encrypted mode-600
backups. Existing business/order row counts, credential ciphertexts and the
encryption key were preserved across the migrations. The v6 helper and journal
hashes remain unchanged. Order configuration revision **6**, manual stock sync,
automatic stock sync and catalog sync are disabled; no automation grant or active
outbox task remains. Temporary publication flags and schedulers were removed.
Latest normal APIs remain on loopback 8080/8082 with one combined worker for user
testing; port 8081's unrelated process was preserved. No provider secrets are in
their environment; the scoped Orders flag remains enabled with configuration off.

The initial [executable manifest](order-contract-manifest.json) byte SHA-256 was
`10a3380688dd9f639ef0c3a4883ec3c481c102a29e5f1fe43446ff70c499a572`;
the final release manifest byte SHA-256 is
`4bb1d2a1ba3454c9e9d7d3f5a6ee60cae031769af5cfe297eb392db15f505ec3`.
Official notices verify [posting versions](https://dev.ozon.ru/news/734-Otkliuchenie-starykh-metodov-Seller-API-dlia-raboty-s-postingami/),
[warehouse/delivery versions](https://dev.ozon.ru/news/648-Izmeneniia-v-metodakh-Seller-API/)
and [label migration/scanit](https://dev.ozon.ru/news/800-Podderzhka-novoi-etiketki-scanit-i-migratsiia-metodov-v-Seller-API/).
Their manifest hashes cover evidence summaries, not downloaded HTML. The full
Ozon schema remained unreadable; observed live payloads do not verify it.
The official [customerorder source](https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/documents/_customerOrder.md)
byte hash is `990e50b153bd64ad0b423710f88fc435564878aa95485b792759415f0563c972`;
the [currency source](https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/dictionaries/_currency.md)
hash is `80cfc5edbc944bd2f3d9d6bdee47206af65990457449d7483a9195be6092bdd8`.
Both returned HTTP 200 on the observation date. Source URLs, dates, full hashes
and value-free structural signatures are retained privately.

Ozon returned 13 warehouses and 10 delivery methods, with separate identities.
No actionable live FBS/rFBS posting was observed. Packing, labels, tracking and
shipment/delivery actions are **NOT_OBSERVED_LIVE**; rFBS stock writes are
**NOT_TESTED**. This test does not establish general production readiness.

Private handoff: `.local/orders-live/final-normal-evidence.json`, its encrypted
full counterpart, `stock-regression-journal.json`, `phase2-journal.json`,
`source-evidence.json`, `mirror-inputs.json`, encrypted payloads/screenshots and
backups. Sensitive customer values and native names remain encrypted or transient;
safe identifiers and counts are separate. The legitimate owner's reset password
is only in the designated private file.
