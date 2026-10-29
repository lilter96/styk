# Controlled live integration lab — 2026-10-29

## Outcome and environment

**SUCCESS for the bounded positive-stock experiment:** Real reads, controlled MoySklad mutations, durable imports, operator-attested cross-system product and warehouse mappings, and the corresponding Shadow Mode view succeeded. Separate, isolated Ozon stock writes for the selected target were acknowledged and provider reads observed **0 → 1 → 0**. The owner explicitly accepted possible real-buyer exposure for this exact one-unit experiment; buyer isolation was not established. These results do not prove causality against other potential writers or readiness for unrestricted stock synchronization.

The starting source revision was `c49879bac0bf8e8cac9745a211da7004f2490099`. The owner identified both connected accounts as fully controlled test accounts and authorized their existing and newly created inventory data for this experiment. The owner separately confirmed the absence of real buyers, customer orders and other integration interference for newly created test offers; that earlier assertion was specific to new offers. The selected offer in this continuation already existed. Before the stock write, the owner explicitly confirmed that no other integration or operator would modify its stock at the selected warehouse during this experiment. That is a targeted owner assertion, not independent provider proof. The owner explicitly authorized the experiment's logical pairing of it with the new MoySklad fixture; this is not independent proof of a shared physical inventory or equivalent sale unit. The owner later expressly authorized temporarily publishing one unit on this exact target even if real buyers could access it, and requested restoration to zero. This is risk acceptance, not evidence of buyer isolation.

The two enabled encrypted credentials were resolved for their registered scopes and matched the local configuration. MoySklad `/context/employee` returned the configured account UUID; Ozon calls used the configured Client-ID and key. To identify the verified scopes without publishing credentials, the SHA-256 fingerprints of the configured account identifiers begin `8fd183ebcacb` for MoySklad and `a3a518ffbc98` for Ozon. Client authentication and local registration do not independently establish legal ownership. Exact account scopes, planned scenarios, returned IDs, mutation intent states, observations, request ledger and the selected target are in the ignored [local fixture manifest](../.local/controlled-live-lab/manifest.json); it contains no credentials or raw provider responses and is local to this workspace. Its unique fixture namespace is `STYK-LIVE-TEST-20261029-4ea8765d`.

The MoySklad account exposed the previously created, unarchived test warehouse and one organization. Both webhook collections were completely traversed within the preflight limit and had no enabled visible webhooks. This is permission-scoped evidence, not proof that no other integration exists. A previously verified test variant characteristic was read by ID and reused without changing it. The MoySklad context reported account administration and stock-entry/loss create and approval permissions; successful operations below show the observed permissions for the other entities and documents.

Ozon returned 13 warehouses: one FBS with status `created`, six FBS with `disabled`, four rFBS with `created`, and two rFBS with `disabled`. The selected warehouse `1020001323428000` reported `warehouse_type=fbs`, `is_rfbs=false`, `is_kgt=false` and `status=created`. Ozon's published warehouse status table maps `created` to active; that status alone does not prove that a stock write for this specific offer will be accepted. No warehouse was created or modified.

## Request budgets and provider outcomes

| Provider and phase | API read attempts | Intentional API writes | Limits |
|---|---:|---:|---|
| MoySklad, initial lab | **42**: 27 manual and 15 worker reads | **10** | 50 reads, 15 writes |
| Ozon, initial lab | **46**: 38 manual and 8 worker reads | **0** | 50 reads, 10 writes |
| Ozon, stock-write continuation | **5** manual reads | **1** | 6 reads, 1 write |
| Ozon, earlier positive-transition preflight | **7** manual reads | **0** | 12 reads, 2 planned writes |
| Ozon, authorized transition continuation | **10** manual reads | **2** | 10 reads, 2 writes |
| Ozon, combined | **68** reads | **3** | Separate budgets per lab phase |

Counts cover all actual provider API network attempts in this lab, including the catalog workers and the continuation, not earlier independent validation. In the initial phase, 64 of 65 manual provider reads returned HTTP 200; one Ozon `/v1/notification/list` read returned HTTP 429 at 11:52 UTC. All 10 MoySklad writes returned HTTP 200. The 23 worker requests completed successfully as reported by the clients; their individual HTTP codes were not separately captured, so they are reported as successful 2xx rather than asserted to be HTTP 200. The initial phase stopped further Ozon requests after the 429 and did not retry that endpoint. The owner then explicitly authorized a fresh, separate bounded request budget for the stock-write continuation. Its five reads and one write each returned HTTP 200. A later positive-transition preflight made seven reads: six returned HTTP 200 and one returned HTTP 403. The authorized transition continuation made ten reads and two stock writes, all HTTP 200. There were no retries. No ambiguous provider write or unresolved external mutation was recorded.

Manual calls used exact provider hosts, no redirect following, one attempt, a 15-second deadline, at least a one-second interval per provider, and bounded compressed and decoded responses. The category tree had an explicit 16 MiB cap; other manual responses and durable imports were limited to 4 MiB. The worker used the same one-second interval, 15-second HTTP timeout and one client attempt, with limits frozen on each import. The outbox permits up to three lease claims. For the continuation, the supervised worker's lease was five minutes, longer than its three-minute job deadline, so it could not reclaim its own running task within the bounded execution; all five imports actually used one claim each. Public documentation and image-hosting HTTP requests are outside provider API totals.

## Created and retained MoySklad fixtures

All names and external codes below use the manifest namespace. Each creation returned HTTP 200 and was independently read by its returned ID. No unrelated entity was modified.

| Scenario | Entity and observed result | Status |
|---|---|---|
| A/E | Product `MS-A`, ID `e7de650f-d389-11f1-0a80-1ab800275063`, in warehouse `7035238c-d31c-11f1-0a80-071500070d0a`. Stock/reserve 0/0 initially; stock entry produced 10/0; later stock loss produced 7/0. Article and selected Ozon offer ID differ. | **SUCCESS** for source, mismatch, and explicit attested pairing |
| B | Product `MS-B`: explicit stock 0 and reserve 0 in the selected warehouse. | **SUCCESS** on MoySklad; dedicated second Ozon mapping **NOT_TESTED** |
| C | Product `MS-C`: 0/0 initially, 10/0 after the entry, then 10/3 after the test order. | **SUCCESS** for source reserve; cross-system attribution **NOT_TESTED** |
| D | Parent `MS-PARENT` and distinct variants `MS-V1`, `MS-V2`; each variant had an explicit 0/0 warehouse row and an imported parent reference. | **SUCCESS** for source identities; live cross-system variant mappings **NOT_TESTED** |
| C support | A dedicated synthetic counterparty and applicable customer order `ORDER-C`. A separate positions GET showed the actual `MS-C` assortment, quantity 3 and reserve 3. | **SUCCESS** |
| A/C support | Applicable `STOCK-ENTRY` document, 10 units each for `MS-A` and `MS-C` in the selected test warehouse. | **SUCCESS** |
| G support | Applicable `STOCK-CHANGE` loss document for three `MS-A` units, created only after the first completed import and a reported stock of 10. | **SUCCESS** |

The provider's warehouse stock report was read before the entry, after the entry, after the order and after the loss. These are separate observations, not an atomic snapshot. Physical quantities were not edited in STYK. No shipment, payment, refund or financial document was created. The order and its position agree with the stock-report reservation, but that does not prove exhaustive reservation attribution or overlap with marketplace obligations; live no-double-subtraction behavior remains **NOT_TESTED**.

The fixtures, test order, counterparty, stock entry and loss are retained for inspection and repeatable validation. No automatic deletion, archival or document reversal was attempted; deletion is not presumed to reverse a posted movement. A neutral synthetic image was also created for a proposed new Ozon card and retained at `.local/controlled-live-lab/assets/neutral-label.png`. It is publicly hosted in the owner-controlled repository's dedicated [test image branch](https://raw.githubusercontent.com/lilter96/styk/styk-live-test-image-4ea8765d/neutral-label.png); anonymous HTTP 200 readback matched the generated file byte-for-byte. The branch contains only the synthetic image and a short fixture README and is retained for the lab.

## Existing Ozon offer and product creation investigation

The selected existing offer is `ATEST-897677`, product ID `2764544165`, SKU `2889277907`, in FBS warehouse `1020001323428000`. Its `is_archived` and `is_autoarchived` fields were false, moderation was `approved`, validation was `success`, `is_created` was true and product status was `price_sent`. Product and warehouse were both non-KGT. The provider returned the exact product–warehouse row with `present=0`, `reserved=0`, and `free_stock=0` at 11:41 UTC. A second existing active candidate was inspected, but the selected offer had no matching line in a bounded 30-day FBS posting sample. A bounded unfulfilled FBS listing for this warehouse, with cutoff from 2025-11-13 through 2026-11-12, returned zero postings and `has_next=false`. This is a scoped sample, not a guarantee about all orders or future activity.

The key's own `/v1/roles` result explicitly listed both `/v2/products/stocks` and `/v2/product/info/stocks-by-warehouse/fbs`. The live read permission was demonstrated; the presence of the stock-write method in the role list does not demonstrate per-target acceptance. The role list also included `/v1/notification/list`, but an attempt to inspect configured notifications returned HTTP 429; notification endpoints and their subscribers therefore remain **UNKNOWN**. That unrelated method was not retried. In the subsequent continuation, fresh reads confirmed the exact product and warehouse, and the existing offer received one zero-stock update; its product-card attributes were not modified.

An earlier stage investigated creating a new paper-label product. Ozon's live category tree, attributes and dictionaries were read; eleven simple stationery types all reported customs-classification and mandatory-marking fields as required. The seller-info response did not establish a VAT rate for a new synthetic offer. Ozon's own notices describe [the required customs-code field](https://dev.ozon.ru/news/690-Seller-API-Atribut-22232-stal-obiazatelnym-dlia-zapolneniia-dlia-mnozhestva-kategorii/) and [the marking field](https://dev.ozon.ru/news/777-Izmenenie-validatsii-atributa-23536-Nuzhen-kod-markirovki-v-Seller-API/). No unsupported tax or legal declaration was supplied, and no new card was submitted. The owner's subsequent authorization to use existing catalog entries made product creation unnecessary for the main scenario. New-product submission, moderation and propagation are **NOT_TESTED**.

## Durable imports and PostgreSQL evidence

The clean-revision API and worker were built separately and run on loopback, with real publication flags absent. Each operation was submitted through the authenticated operator API, inserted its durable outbox task, and was executed by the worker against the real provider. The following batch IDs refer only to the local PostgreSQL database:

| Operation | Outcome | Batch | Batch created | Provider attempts / claims | Persisted traversal |
|---|---|---:|---|---|---|
| Initial MoySklad | **SUCCESS**, `COMPLETE_TRAVERSAL` | 3 | yes | 5 / 1 | 13 products, 4 variants, 3 warehouses, 17 aggregate and 51 warehouse stock observations |
| Initial Ozon | **SUCCESS**, `COMPLETE_TRAVERSAL` | 2 | no, identical eligible batch reused | 4 / 1 | 33 products, 13 warehouses, 5 stock observations |
| Changed MoySklad | **SUCCESS**, `COMPLETE_TRAVERSAL` | 4 | yes | 5 / 1 | Same entity counts; `MS-A` now stock 7, reserve 0 |
| Continuation MoySklad | **SUCCESS**, `COMPLETE_TRAVERSAL` | 4 | no, identical batch reused | 5 / 1 | Exact fixture quantities retained |
| Continuation Ozon | **SUCCESS**, `COMPLETE_TRAVERSAL` | 2 | no, identical batch reused | 4 / 1 | Selected existing offer and its stock observation retained |

PostgreSQL links each successful operation and succeeded outbox task to its completed batch. The two MoySklad batches contain all six new product/variant identities and both parent links. Warehouse stock values for `MS-A`, `MS-B` and `MS-C` agree with provider observations; warehouse-level available `quantity` remains NULL. The authenticated `/api/stock` read also returned the changed `7/0`, explicit `0/0`, and reservation `10/3` values. Reuse of identical batches did not refresh their original observation timestamps; a completed traversal does not prove an exhaustive or consistent snapshot, and an omitted stock row remains unknown.

## Mapping and Shadow Mode

Before confirmation, the imported `ATEST-897677` offer had `NO_CANDIDATE` and zero deterministic product candidates, so the different identifiers did not fabricate a match. The operator created and confirmed a review pairing it with the exact `MS-A` product. PostgreSQL contains product mapping **2/version 1**, its review, history and confirmation audit, classified **`OPERATOR_ATTESTED`** and `LOCAL_OPERATOR_SESSION`, linked to Ozon batch 2 and MoySklad batch 4. The explanation records owner-authorized logical fixture pairing and sale-unit equivalence as operator assertions.

A selected rFBS warehouse was classified `is_rfbs=true`; creation of its warehouse review returned HTTP 400 `INVALID_REVIEW`, so it could not reach confirmation. The selected FBS warehouse and the designated MoySklad test warehouse were reviewed and confirmed through the normal authenticated workflow. PostgreSQL contains warehouse mapping **1/version 1**, its review, history and confirmation audit, likewise **`OPERATOR_ATTESTED`** and `LOCAL_OPERATOR_SESSION`, originally linked to Ozon batch 2 and MoySklad batch 3. A separate review of that confirmed pair was `CURRENT` after the first imports and became `STALE_SOURCE` after batch 4; the original mapping remains in history.

The real Shadow API and browser dialog displayed the selected target with both mappings confirmed: physical stock **7.0**, reserve **0.0**, Ozon free stock **0**, and calculated quantity **unknown**. The screen displayed “Нет данных” for the missing candidate rather than treating it as zero, showed both batch timestamps and stated that sending to Ozon is disabled. The reused MoySklad batch finished at 11:18:42 UTC and the reused Ozon batch at 09:52:27 UTC on 2026-10-29. The Shadow candidate matched the authoritative stock-sync review quantity, which is generated using the existing `commitment.Evaluate` path; both were null. This does not test an independently computed numeric candidate.

The target was **BLOCKED** for missing or stale evidence, including competing-exposure verification, customer-order import and attribution, sale-unit verification, source baseline and ordering, posting coverage, unassigned reserves, unreflected obligations and reduction bounds, safety stock, and source and Ozon traversal freshness. These are missing evidence or operator assertions and unsupported business semantics; no specific calculator or parser defect was observed. Existing synthetic tests separately cover reservation handling, parent/variant exposure, and the UI distinction between zero and missing, but they do not establish live order attribution.

## Controlled Ozon stock-write experiment

**SUCCESS — one bounded request acknowledged, with no causal change demonstrated.** The owner confirmed that other integrations and operators would not change the exact existing offer's stock at the selected warehouse during this experiment. The registered Ozon account's enabled encrypted credential again matched the local configured test credential. A fresh product read returned `price_sent`, `approved`, non-archived, non-KGT and the expected SKU. A fresh warehouse read returned `created`, FBS and non-KGT. At 12:01:59 UTC the exact stock read returned present **0**, reserved **0**, free **0**. The bounded earlier unfulfilled-posting sample remained the available order evidence; it is not an exhaustive proof of future activity. The update introduced no positive selling stock.

The previous HTTP 429 concerned only `/v1/notification/list`; its response headers were not retained at the time. Ozon [documents `Retry-After` for 429 and `Ratelimit-Remaining`](https://dev.ozon.ru/news/802-Zagolovki-dlia-upravleniia-reit-limitami-v-Seller-API/). More than seven minutes elapsed before this separate continuation, and the notification request was not repeated. The required product, warehouse and stock reads returned HTTP 200; thus those endpoints were available, while the notification endpoint's later status was not tested. The fresh reads reported remaining limits of 9 for product information and 49 for warehouse and stock information.

Ozon's [stock guidance](https://dev.ozon.ru/start/299-Rekomendatsii-po-upravleniiu-ostatkami-v-Seller-API/) identifies `/v2/products/stocks`, free-stock semantics and rate limits; its [replacement notice](https://dev.ozon.ru/news/648-Izmeneniia-v-metodakh-Seller-API/) identifies `/v2/product/info/stocks-by-warehouse/fbs` for readback. The Seller API Swagger was fetched from its [official origin through a translation proxy](https://docs-ozon-ru.translate.goog/api/seller/swagger.json?_x_tr_sl=ru&_x_tr_tl=en&_x_tr_hl=en), because direct retrieval returned HTTP 403. Its prose requires `price_sent`, says one pair may be updated once per 30 seconds, and advises using only one of offer ID or product ID. Its example uses `stocks` with `product_id`, `stock`, and `warehouse_id`. The item schema also lists `quant_size` as required but neither defines that property nor includes it in the example; the diagnostic did not invent a value. Proxy retrieval is an intermediary and does not independently guarantee the document's freshness.

At 12:02:12 UTC, the ignored local diagnostic sent one `/v2/products/stocks` request, containing only product `2764544165`, warehouse `1020001323428000` and stock **0**. Its exact-account, exact-product, exact-warehouse and zero-quantity allowlist, exclusive durable intent journal and zero-retry policy were retained. The journal recorded `UNKNOWN` before dispatch and `ACKNOWLEDGED` only after Ozon returned HTTP **200**, the exact target with **`updated=true`**, and **no errors**. The write response's `Ratelimit-Remaining` was 49. No additional target or second write was sent, and no normal application publisher was used.

Readback of the exact target returned HTTP 200 with present **0**, reserved **0**, free **0** at **12:02:15 UTC** and again at **12:02:38 UTC**. Both responses reported `Ratelimit-Remaining: 49`. The matching readbacks corroborate the observed state, but cannot prove that the request caused a change because the value was already zero. Positive-stock behavior, ordering against real orders, competing writers beyond the owner's assertion and production reconciliation remain unproven. The target and durable journal are retained; no cleanup or replay was attempted.

The local diagnostic and read helper are ignored, unavailable through the browser and separate from the application and worker. Four offline synthetic-transport tests passed, including exact-target rejection, old versus current rate-limit fencing, write-budget enforcement, durable `UNKNOWN` before dispatch, lost-response handling and refusal to resend. The continuation did not repeat catalog imports, create fixtures, or modify mappings.

## Positive stock transition preflight

**Initially BLOCKED — neither transition was dispatched in this preflight.** A subsequent experiment planned two separately journaled stock intents for the same Ozon offer and FBS warehouse: **0 → 1** followed, only after an acknowledged and observed first transition and renewed isolation checks, by **1 → 0**. The existing zero-stock journal was inspected and its SHA-256 verified unchanged. At that stage, both newly planned intents were `NOT_DISPATCHED`, and no new dispatch journal had been created. The prior successful **0 → 0** acknowledgment remains separate evidence.

The exact registered test-account credential was rechecked. On 2026-10-29, fresh provider reads returned the selected product as non-archived, approved and `price_sent`, with `has_price=true` and `has_stock=false`; the warehouse was active FBS. At **12:10:26 UTC**, the exact stock row showed present **0**, reserved **0**, free **0**. A bounded unfulfilled FBS posting query for the warehouse returned zero postings and no next page; this does not prove there can be no future order. The provider's visibility read returned `showcases_visibility=OZON` for the SKU at **12:10:33 UTC**. That is a storefront placement observation, not evidence that a positive-stock offer cannot be purchased.

The owner had confirmed the absence of competing modifications for the target during the previous experiment and indicated that a verifiable buyer-access restriction exists, but had not yet supplied its mechanism. No buyer exclusion for this existing positive-stock target was independently established. In addition, `/v1/seller/ozon-logistics/info` returned HTTP **403** at **12:10:36 UTC**, despite the method being listed in the key's roles. Under the lab rule to stop after authorization errors, no further Ozon call or write was made. The seven preflight requests comprised six HTTP 200 reads and that one HTTP 403 read; no request returned 429, and the response reported `Ratelimit-Remaining: 49`. The request budget was 12 reads and two writes; actual writes were **0**.

Ozon announced a buyer-isolated [Seller API sandbox](https://dev.ozon.ru/start/562-Sandbox-v-Seller-API-novye-vozmozhnosti-dlia-integratsii/) for Ozon Delivery scenarios. Its published Sandbox API schema, retrieved from the official documentation origin through a translation proxy, lists FBS stock reads but does not list `/v2/products/stocks`. The current local environment does not have configured sandbox OAuth client credentials. Thus a supported sandbox stock-update target and contract were not established; the endpoint was not guessed or called.

At the end of that preflight, the latest observed target state was **0/0/0**, with no positive write or restoration attempt and no unknown external write outcome. The prior zero-to-zero response could not establish positive-stock or transition semantics. The later owner-authorized continuation is recorded below.

In the next follow-up review, the owner clarified that the HTTP 403 applies to the Ozon Delivery information method and does not, by itself, invalidate unrelated stock permissions. That method was not retried. No additional provider requests or writes were made: the existing `OZON` showcase observation and the owner’s pending clarification did not establish which control excludes real purchases after a positive stock update. A provider-enforced restriction applicable to this exact offer while stock is positive, or another verifiable buyer-isolated target, remains necessary before the transition can be attempted.

## Owner-authorized positive stock transition

**SUCCESS — 0 → 1 → 0 observed and both writes acknowledged.** The owner explicitly authorized this exact account, offer, SKU and FBS warehouse to publish at most one unit temporarily, accepting possible exposure to real buyers. No buyer-isolation mechanism was claimed or required for this bounded experiment. The previous Ozon Delivery information HTTP 403 was treated as specific to that endpoint; it was not retried or bypassed. The account's stored credential was matched again, and fresh reads confirmed the product as approved and `price_sent`, the warehouse as active FBS and the initial stock as zero.

| Time, UTC (2026-10-29) | Operation or observation | Present / reserved / free | Provider result |
|---|---|---|---|
| 12:33:34.575 | Initial stock read | **0 / 0 / 0** | HTTP 200 |
| 12:33:44.465 | Single update to **1** | — | HTTP 200; exact target `updated=true`, `errors=[]` |
| 12:33:47.565 | First positive readback | **1 / 0 / 1** | HTTP 200 |
| 12:34:18.836 | Fresh pre-restoration read | **1 / 0 / 1** | HTTP 200 |
| 12:34:23.570 | Separate update to **0** | — | HTTP 200; exact target `updated=true`, `errors=[]` |
| 12:34:27.741 | First restoration readback | **0 / 0 / 0** | HTTP 200 |
| 12:34:44.521 | Later restoration readback | **0 / 0 / 0** | HTTP 200 |

The first observed positive state was about **3.1 seconds** after request start; the first observed restored state was about **4.2 seconds** after restoration request start. These are observation delays, not exact application latencies. The writes were approximately **39.1 seconds** apart, exceeding Ozon's documented 30-second minimum for a product–warehouse pair. The continuation used **10 reads and 2 writes**, all HTTP 200, within its separate 10-read/2-write budget; there were no retries. The stock-update responses each reported `Ratelimit-Remaining: 49`.

Bounded unfulfilled FBS checks before the positive write, immediately before restoration and after restoration each returned zero selected-target lines. All observed reserves were zero. Those checks are not exhaustive proof of the absence of orders outside the sampled window or of future activity. The owner had attested that other operators and integrations would not modify the selected target during the experiment; that remains an operator assertion rather than independent proof. The timed observations and per-target acknowledgments provide strong evidence of the selected 0-to-1 and 1-to-0 transitions, consistent with an absolute free-stock update, but matching readbacks alone do not exclude another causal influence or establish general synchronization correctness.

The ignored, local-only diagnostic used the fixed account, product and warehouse with quantity **1** for the first intent and **0** for the second. It created a separate exclusive, fsynced `UNKNOWN` intent before each dispatch and a separate immutable outcome record afterward; both outcomes are `ACKNOWLEDGED`. It sends one target per request and has no automatic write retry. The earlier zero-stock journal was verified unchanged. Four offline tests covered exact-target restrictions, journaling before dispatch, lost-response fencing, no replay, restoration gating, no orders/reservations and the minimum interval. No product-card attributes, warehouse settings, mappings, customer orders or application publication configuration were changed in this continuation.

## Safety and verification

The normal application real-host publication hold remains enabled. An authenticated local enable request returned HTTP 503 `STOCK_PUBLICATION_DISABLED`. The worker was started with no publication or simulator flags; the source adapter still restricts write-enabled clients to an explicit loopback simulator. PostgreSQL contained zero publication operations, zero publication targets and zero active outbox tasks after the lab. No recurring synchronization or automatic marketplace publication was enabled. An already in-flight request or other host's processes were not assessed.

Verification passed: `make lint`, `make test`, `go test -race ./...`, `make build`, migrations through 24 and `make test-integration` on a dedicated local test database, targeted publication and Shadow PostgreSQL tests under `-race` on a separately named dedicated test database, three credential/account-isolation integration tests under `-race` on the dedicated credential test database, all 117 frontend unit tests, the synthetic browser Shadow regression, the selected live Shadow browser view, and the isolated diagnostic's offline tests. The earlier mapping continuation also reran the Ozon adapter, configuration and commitment tests under `-race` and the dedicated publication/Shadow integration suite under `-race`. No observed parser incompatibility justified a production code change or a raw-response fixture.

The prior [live read validation](live-marketplace-validation-2026-10.md) remains evidence of successful read-only integration. This lab additionally demonstrates controlled MoySklad mutations, durable reimports, reviewed cross-system mappings, a blocked live Shadow calculation, and acknowledged Ozon updates with an observed 0 → 1 → 0 transition on one target. It does **not** independently prove causality against other writers, general production reconciliation or unrestricted synchronization readiness. Remaining publication risks and the existing hold are documented in the [stock publication safety audit](stock-publication-safety-audit.md).

## End-to-end test-account publication attempt

**PARTIAL / UNKNOWN — the 7 → 3 → 0 scenario did not complete.** A fixed, default-off test policy was added to the normal application, worker, PostgreSQL outbox and Ozon adapter. It authorizes only the registered `cross-e2e` account pair, the previously confirmed product/warehouse pair and quantities 0–7. Each publication requires a fresh operator review; no recurring publication is enabled. The operator explicitly accepted possible buyer exposure. The risk assumptions used for this test were persisted as `OPERATOR_ATTESTED`, including assumed zero unreflected obligations, unseen reductions and safety stock, one-to-one sale units, and no competing exposure. They are not independent evidence that the source traversal is atomic or order coverage complete.

On 2026-10-29, the browser initiated durable imports through the normal worker. MoySklad operation `920aff31faf27ed453a6ddc87bce5b29` completed in five provider attempts and reused batch 4; Ozon operation `17346a1ca657e1e6d073761d39204dea` completed in four attempts and reused batch 2. Their original observation timestamps were preserved, while the completed traversals supplied separate fresh-traversal evidence. The selected source showed physical **7**, reserve **0**; Ozon showed free **0**, reserve **0**. The existing calculator produced candidate **7** for review `9a7c52b2e40a892523655ca44150d74a` against the existing confirmed mappings. The separate reservation fixture was still observed at physical **10**, reserve **3**; it was not published in this run.

The operator confirmed the review in the web UI. Publication `abe7a389b26dbf8cdd36954308ee538d` and its one-attempt outbox task were committed, and the worker claimed and attempted one stock request for **7** at 13:23:40 UTC. **No Ozon HTTP status or per-target acknowledgment was obtained.** The operation is `UNKNOWN`, and its outbox task is `failed`, attempts **1/1**. The local enable API's HTTP 200 was only confirmation of local intent creation, not an Ozon response. No write was retried.

An independent exact-target stock read at 13:24:12 UTC returned HTTP 200, **present 0 / reserved 0 / free 0**. A later read through the STYK reconciliation UI at 13:27:08 UTC persisted **free 0** and `MISMATCH` in the append-only readback table. This observation does not settle whether the earlier external request could still have an effect; the operation remains `UNKNOWN` and fenced. No new MoySklad loss document was created, and neither the 7 → 3 nor the 3 → 0 source change was attempted. The expected later Ozon publications were not attempted.

Read-only troubleshooting reproduced a transport defect: the controlled Go transport negotiated ALPN `h2` while its HTTP/2 handler was disabled, producing a malformed HTTP response on the stock-read endpoint. The failed write did not record a transport trace, so the reproduced defect is a likely explanation, not proof of its individual external outcome. The client was fixed to advertise HTTP/1.1, with a regression test against an HTTP/2-capable local TLS server. A subsequent real read using the corrected controlled client returned free **0**. This fix does not release or replay the prior UNKNOWN operation.

The continuation budget was Ozon **50 reads / 3 writes** and MoySklad **50 reads / 2 writes**. Actual network attempts were Ozon **9 reads and one write attempt**, and MoySklad **5 reads and no writes**. Of the Ozon reads, four were the completed import, one was the HTTP 200 direct read, two were read-only transport probes that failed without a usable HTTP status, one was the successful fixed-client read and one was the successful STYK reconciliation read. The local test setting was disabled after inspection; the test worker and temporary API were stopped. The existing zero-stock diagnostic journal remained unchanged. The earlier standalone 0 → 1 → 0 experiment remains separate evidence and is not counted as a successful STYK publication.

Synthetic verification covered the browser, real Go API and worker, PostgreSQL and a deterministic loopback provider. The browser selected the accounts, imported both catalogs, created and confirmed product and warehouse mapping reviews, recorded operator assumptions, reimported, reviewed candidate **7**, confirmed one outbox dispatch and observed matching reconciliation. Separate tests covered concurrent claims, expired lease, HTTP 403/429/500, lost response, per-target errors and mismatching readback. Desktop and mobile views were inspected. A production-wide publication hold remains in place; the test-account exception and unresolved remote-ordering limits are described in the [safety audit](stock-publication-safety-audit.md).

## Audited recovery and real end-to-end synchronization

**SUCCESS for the bounded test sequence; the earlier external outcome remains UNKNOWN.** With the previous worker stopped, the operator used the UI to perform a fresh read of the exact target and explicitly accept the possibility that the earlier request could still apply later. The observation was free **0** at 13:48:36 UTC. Immutable recovery event `f202a43cde98978b9286e9c69aec142f` records the local operator session, target, observed value and time, reason, worker-stopped assertion and accepted uncertainty. It is linked to the first new publication. Original operation `abe7a389b26dbf8cdd36954308ee538d` remains **UNKNOWN**, with its failed, single-attempt outbox task unchanged; it was neither retried nor represented as successful.

The corrected normal-worker transport passed both the local TLS regression and a real read-only Ozon stock request. New operator-attested conditions and completed traversals of both providers were recorded after the recovery decision. Each following row is a **new** reviewed operation through the normal STYK API, PostgreSQL outbox and worker. Ozon returned HTTP **200**, `updated=true` and `errors=[]` for every row; matching readbacks were persisted as `RECONCILED`.

| MoySklad physical / reserve | Candidate | Publication operation | Ozon dispatch, UTC | Ozon free readback, UTC |
|---|---:|---|---|---|
| 7 / 0 | 7 | `2dedce449f5f76b1f7811dbeaa5ea29a` | 13:48:49.034 | 7 at 13:48:50.195 |
| 3 / 0 | 3 | `2f2affc58e13c6296e48bc263daa6606` | 13:49:19.828 | 3 at 13:49:20.967 |
| 0 / 0 | 0 | `fea8948277261c0cb3280a6158b294d8` | 13:49:50.696 | 0 at 13:49:52.156 |

The dispatches were approximately **30.79** and **30.87 seconds** apart. The existing product mapping 2/version 1 and warehouse mapping 1/version 1 were reused. MoySklad loss documents `7c5e256a-d39f-11f1-0a80-07150032f821` and `8f16a5e3-d39f-11f1-0a80-1b920031f558` were created through the provider API with HTTP **200** and independently checked through stock reports: physical stock changed **7 → 3 → 0**, with reserve **0** at each observation. No source document was created before the preceding Ozon publication was reconciled.

The relevant completed MoySklad import operations were `05f9416c58f1367507d921d2bdbbb1f5`, `25f639232305d1ec6481ebb3b2421fc2` and `811b679ed548a29693db735b7bb0bb1d`, linked to batches **4**, **6** and **8** respectively; batch 4 reused identical content. Ozon imports `6735fbffe84d72c44105d9715a3fc778`, `4c7e435f63632b3ba4a1f20b2d20f8be`, `5cefce51604acd2d8c03027a3094ec08` and `6563ccf56efaf1762fdb3a62945c334c` linked to batches **2**, **5**, **7** and **9**. The persisted Ozon observations were **present / reserved / free**: `0/0/0`, `7/0/7`, `3/0/3`, `0/0/0`. The separate source reservation fixture was not published in this sequence.

The fresh continuation budget was Ozon **50 reads / 3 writes** and MoySklad **50 reads / 2 writes**; actual attempts were Ozon **21 reads / 3 writes** and MoySklad **21 reads / 2 writes**. No new write had an unresolved outcome. The browser displayed the recovery decision and final reconciled zero on desktop and mobile; both were inspected. A synthetic browser test also exercised UNKNOWN → explicit recovery → fresh imports and review → separate worker publication → reconciliation, and dedicated race tests covered simultaneous recovery requests. The local test sync setting was disabled and the temporary worker and API were stopped afterward; the original and earlier lab journals were preserved.

The owner accepted possible real-buyer exposure and the remaining risk of a late effect from the old UNKNOWN request for this exact test target. No later observation in this run contradicted the three new reconciled results, but the database lease, readback and operator decision do not establish remote ordering or causality. This controlled test does not enable recurring publication or establish readiness for arbitrary production accounts.

## Automatic test-account synchronization

**SUCCESS for the selected test target.** On 2026-10-29 the operator enabled a versioned, `OPERATOR_ATTESTED` policy for the existing account pair and mappings, with a 45-second interval, 30-minute validity, safety stock and assumed unreflected obligations of zero, and a maximum of seven units. The owner accepted possible buyer exposure. The scheduler created durable cycles and provider imports; the existing calculator, publication outbox and normal worker made each decision and write. No import or publication was manually triggered during the sequence.

| MoySklad physical / reserve | Candidate | Automatic cycle | Publication | Ozon free readback, UTC |
|---|---:|---|---|---|
| 5 / 0 | 5 | `a1080803ba0fde7f320d458d37b916b2` | `6b4e1736cef387d2116cf3ee7a49e229` | 5 at 14:28:30 |
| 2 / 0 | 2 | `09a5e9686eed582bd8acf496fe6a16ac` | `2a81960a3b1248567d3f0e1c3fc26931` | 2 at 14:29:24 |
| 0 / 0 | 0 | `42384af459191f61422846c4a3bc87dc` | `2d554de8d71d5cd5356ddaa370c86f5c` | 0 at 14:30:19 |
| 1 / 0, after pause | 1 | `7853eb802b8a03dbd78853e7c5d6f53e` | `acff5986976a81ee27fe43e6a87db684` | 1 at 14:33:19 |
| 0 / 0, restoration | 0 | `180c2233004c3b7a01490ff797c8ec11` | `302af508d68bef72f0ca4cb90b1aec67` | 0 at 14:34:14 |

All five writes returned HTTP **200**; each outbox task succeeded on its first attempt and each publication is **RECONCILED**. The then-current adapter could acknowledge a target only after parsing `updated=true`, `errors=[]`, so those fields follow from the recorded acknowledgement and the adapter's behavior. The raw response and individual fields were not independently persisted for these historical operations. The real MoySklad entry/loss documents, in order, are `e4b1f16e-d3a4-11f1-0a80-146d00350a3f`, `0586e351-d3a5-11f1-0a80-08cd0034449d`, `263f61e7-d3a5-11f1-0a80-1e5f00340f6a`, `8687f53f-d3a5-11f1-0a80-10b2003529d6` and `b25b2dcc-d3a5-11f1-0a80-1b9200342963`; each returned HTTP 200 and the source quantity was independently observed after creation.

Two subsequent scheduled no-op cycles, `fcb6fa756d6a09e9de151e491e7b20e0` and `d1a0c4f44f93cc25909d95d13989e188`, reused MoySklad batch **13** and issued no writes. The operator then paused automatic mode, changed the source from 0 to 1 and observed no cycle or publication for 60 seconds. Re-enabling detected and reconciled 1; a final source loss and automatic publication restored zero. A final independent exact-target Ozon read returned HTTP 200, **present/reserved/free 0/0/0**. This observation is not an exhaustive proof that no order exists or that other writers cannot act later.

The bounded continuation used **50 MoySklad reads / 5 writes** and **43 Ozon reads / 5 writes**, including automatic imports, readbacks and exact-target checks; no rate-limit response was observed. The automatic policy and ordinary test publication setting were disabled afterward, and the temporary worker and API were stopped. The original superseded operation remains UNKNOWN; no new publication remains unknown. Desktop and mobile browser views were inspected. A local-only manifest and journal retain the operation details without credentials.

The live run used the initial scoped scheduler and console implementation. Subsequent generalization for independently selected targets, loopback multi-account tests and persistence of sanitized per-target provider results was verified with deterministic local providers; it was not followed by another live write because the MoySklad read budget had been exhausted. Migration 28 does not backfill the new per-target result fields for the historical live operations. Test-policy assumptions retain their original operator-attestation time and expire; provider traversals are fresh-read evidence, not atomic watermarks. The earlier cycles' `source_lag_ms` measured traversal age, not true change-to-reconciliation lag; the latter is unavailable from current source evidence. Unknown provider codes are sanitized rather than stored as untrusted text. General production publication remains disabled.

## Multi-product automatic test (2026-10-29)

**SUCCESS for two independent targets.** The existing account pair and warehouse mapping `1/v1` were used. Ordinary source `e7de650f-d389-11f1-0a80-1ab800275063` remained mapped to Ozon `ATEST-897677` (`2764544165`, mapping `2/v1`). Reserved source `ea33e770-d389-11f1-0a80-0d480029c772` was explicitly mapped through the application to `ATEST-0010987` (`2764939447`, mapping `3/v1`). The new mapping is `OPERATOR_ATTESTED`; matching identities or test ownership do not independently establish business correspondence. Both used source warehouse `7035238c-d31c-11f1-0a80-071500070d0a` and Ozon FBS warehouse `1020001323428000`.

The actual applicable test order `f39a9ec4-d389-11f1-0a80-1b92002840db`, status `Новый`, had one position for the reserved source with quantity and reserve 3. The aggregate stock report returned physical 10, reserve 3 and available quantity 7; the warehouse report returned physical 10 and reserve 3. The existing calculator used physical stock and subtracted the reserve once. Order positions and reservation updates were independently read from the provider for this experiment; they were not fabricated as an imported order-correlation result in STYK. Coverage of other obligations, attribution and competing exposure remained expiring operator attestations. [MoySklad's stock documentation](https://support.moysklad.ru/hc/ru/%D1%82%D0%BE%D0%B2%D0%B0%D1%80%D1%8B%20%D0%B8%20%D1%81%D0%BA%D0%BB%D0%B0%D0%B4%D1%8B/%D0%BE%D1%81%D1%82%D0%B0%D1%82%D0%BA%D0%B8%20%D0%B8%20%D1%81%D0%B5%D0%B1%D0%B5%D1%81%D1%82%D0%BE%D0%B8%D0%BC%D0%BE%D1%81%D1%82%D1%8C/%D1%81%D0%BA%D0%BB%D0%B0%D0%B4%D1%81%D0%BA%D0%BE%D0%B8_%D1%83%D1%87%D0%B5%D1%82_%D0%BE%D1%81%D1%82%D0%B0%D1%82%D0%BA%D0%B8_%D1%80%D0%B5%D0%B7%D0%B5%D1%80%D0%B2%D1%8B_%D0%BE%D0%B6%D0%B8%D0%B4%D0%B0%D0%BD%D0%B8%D1%8F) states that reserve reduces available quantity rather than physical stock. The [official API SDK](https://github.com/moysklad/remap-1.2-php-sdk/blob/master/docs/Api/CustomerOrdersApi.md) documents the position update endpoint used for the test order.

| Cycle | A: source physical/reserve → candidate → Ozon readback | B: source physical/reserve → candidate → Ozon readback | Publication IDs |
|---|---|---|---|
| `6a7c90f98c062a974da67e595821db61` | 0/0 → 0 → 0; no write | 10/3 → 7 → 7 | `0a10017bd9b40196c2046a06f4cfbfea` |
| `6a02fd0f0827aecbd10b4da50d71bef9` | 4/0 → 4 → 4 | 10/3 → 7 → 7; no write | `1dad4ab1f93e85523ef2151619d224e7` |
| `6e698b7c642a7c98afd6844b5ec9870c` | 2/0 → 2 → 2 | 10/4 → 6 → 6 | `9aab62e97b4fa821225f0ff72fc25f76`, `f37d0b072e8590230eb15723a9130ed5` |
| `b66a1503bbc25c3342c3da18da15e0d8` | 0/0 → 0 → 0 | 10/0 → 10 → 10 | `039e2995a90821a45ecbe696dd68526b`, `2d0b34846c2f3e4265dc24ff14549708` |
| `f0a8991ca6d8200448949c8fd7688db0` | 0/0 → 0 → 0; no write | 10/3 → 0 → 0, with safety stock 7 | `16d2eb08f9556bc344bf8601c357b9f1` |

All seven normal-worker publications had one successful outbox attempt, HTTP 200, persisted `provider_updated=true`, `provider_error_codes=[]` and a matching readback; all reached `RECONCILED`. The two cycles containing two changed targets each used **one** MoySklad and **one** Ozon import, then two separate publication operations. Cycles took approximately 8.8–25.8 seconds, including the per-target update interval; each shared import used five MoySklad and four Ozon requests, plus one Ozon readback per write. The ordinary 0 → 4 change took 52.7 seconds from the observed document acknowledgment to readback; reservation release took 50.6 seconds from its verified position read to readback. These are measured local observation intervals, not true provider-change-to-reconciliation latency: neither the provider's exact change time nor an atomic source watermark is available.

The ordinary product changed 0 → 4 → 2 → 0 through applicable stock documents `134f4462-d3af-11f1-0a80-08cd0037c4c0`, `3406f800-d3af-11f1-0a80-1ab80036cc7f` and `607cf339-d3af-11f1-0a80-0b9a003856a7`. The test order's position reserve changed 3 → 4 → 0 → 3 and each update was read back; its position quantity was restored to 3. After the ordinary-only change, the reserved offer received no write. During a 62-second pause the ordinary stock and test reservation changed without a new cycle or publication; resuming processed both in the same cycle. Two later no-op cycles `8d438af5c00d86a5faa5c58637834098` and `e1d69dbd74a525c942b028e1c01d4787` reused source batch 26 and sent no writes.

The bounded V2 continuation used **46 MoySklad reads / 6 writes** and **39 Ozon reads / 7 writes**; one preliminary MoySklad request returned HTTP 400 before correcting the `Accept` header, and the rest of the recorded provider requests succeeded without a 429. After the policy was disabled and temporary services stopped, independent provider reads confirmed source A physical/reserve **0/0**, source B **10/3**, and Ozon present/reserved/free **0/0/0** for both targets. No new publication remained UNKNOWN. The older superseded UNKNOWN remains historically unresolved and is not a remote-ordering guarantee.

**Third target: BLOCKED for a live publication.** The zero-stock source product exists, and other non-archived Ozon offers exist, but the provider returned no stock row for them at the account's only active FBS warehouse. An omitted row is not an observed zero; this is a limitation of the available evidence and current eligibility checks, not proof that Ozon could never support the offer. The independent zero-stock target is exercised with deterministic local provider fixtures. The browser was checked on desktop and mobile against both local providers and the real account data. Production publication remains disabled.

The live writes used commits `2598172` and `06be529`. Afterward, a recovery test found that the older database trigger still limited UNKNOWN supersession to the original target. A later migration extends that guard only to exact targets present in an immutable, operator-audited test policy; it retains the original operation and failed task as UNKNOWN and still requires a fresh bounded readback and a stopped worker. UI handling for missing observations, individual recovery and backlog metrics was also refined after the live writes and checked with local fixtures; the final live UI was read-only. No later real stock write was used to validate those refinements.

## Seller onboarding and first stock publication — V3

**Live result: SUCCESS for the selected test targets; production publication remains disabled.** The seller flow reused existing account registrations, the warehouse mapping and the two earlier product mappings. A third mapping was confirmed through the browser as `OPERATOR_ATTESTED`: MoySklad product `e8f82883-d389-11f1-0a80-1d6400282a1d` → Ozon offer `ATEST-897900`, product `3531908963`, SKU `3468089643`, mapping `4/v1`. The Ozon FBS warehouse was `1020001323428000`, warehouse mapping `1/v1`. The seller's acceptance of this test correspondence and competing-exposure assumptions is not independent verification; the account is not assumed to be a provider sandbox.

Before the first publication, Ozon's complete stock response contained **no row** for this offer and warehouse. The product was non-archived, approved and `price_sent`, and the warehouse was active FBS. The UI recorded a separate one-time authorization for the unknown prior balance **and reserve**; neither was presented as zero. A MoySklad stock-entry document `b702bd33-d3b5-11f1-0a80-1d6400394706` changed the source warehouse from 0/0 to 1/0, and stock-loss document `f3463339-d3b5-11f1-0a80-0d48003ba3f5` later restored 0/0. Both document requests returned HTTP 200 and the source stock changes were observed. [Ozon's official guidance](https://dev.ozon.ru/news/271-Daidzhest-obnovlenii-noiabr/) requires `price_sent` before setting availability; its [stock guidance](https://dev.ozon.ru/start/299-Rekomendatsii-po-upravleniiu-ostatkami-v-Seller-API/) documents the product–warehouse update and 30-second interval. A missing row alone does not prove a zero balance or an absence of reservations.

The normal scheduler, durable imports, existing calculator, outbox and worker produced these real Ozon operations. Each persisted HTTP 200, `updated=true`, no provider errors, one attempt and a matching readback; all reached `RECONCILED`:

| Target and calculation | Publication | Readback |
|---|---|---|
| First publication `ATEST-897900`: source 1/0 → 1, prior marketplace row absent | `2704eced1fdae8a57ed279c910cc410c` | 1 at 16:28:59 UTC |
| Same target: source 0/0 → 0 | `ab6b6288cc746858dc03cb121d794e7e` | 0 at 16:30:36 UTC |
| Reserved target `ATEST-0010987`: source 10/3 → 7 | `fd12b8990983872dfe2a79bac952dfce` | 7 at 16:32:08 UTC |
| Reserved target: source 10/3 with safety stock 7 → 0 | `e01b311d8790b65942cfe8ef9a19ca2f` | 0 at 16:32:39 UTC |

The first target's later Ozon stock reads returned a real row; the first intent remains separately recorded in `stock_initial_publications`. A cancelled intent that was never dispatched can be replaced only after a new, separately acknowledged policy revision; any prior dispatched or UNKNOWN intent remains fenced against automatic first-publication replay. Both modified Ozon targets ended at observed free stock 0. The temporary automatic policy was disabled at revision 15, the test worker stopped, and no new active publication fence remained. A matching readback is an observation of state, not proof that no other writer could have contributed. The old superseded UNKNOWN operation remains historically UNKNOWN.

The bounded order-evidence workflow persisted a one-hour MoySklad traversal as batch 1 and an Ozon FBS posting traversal as batch 2. Ozon lists `/v4/posting/fbs/list` among its [current posting methods](https://dev.ozon.ru/news/734-Otkliuchenie-starykh-metodov-Seller-API-dlia-raboty-s-postingami/). The existing applicable test order `f39a9ec4-d389-11f1-0a80-1b92002840db` contained the reserved target's position with ordered quantity 3, reserve 3 and shipped quantity 0. The calculator used the aggregate warehouse reserve once; the position was not subtracted again. No matching Ozon posting appeared in that bounded window, which does **not** establish absence of other obligations or prove overlap. Coverage and attribution remain expiring `OPERATOR_ATTESTED` test assumptions. A prior seven-day MoySklad check used its full 15-attempt bound and returned unavailable without saving a partial batch; the precise cause was not recorded. The one-hour check completed in three MoySklad attempts and one Ozon attempt.

This continuation used **50 MoySklad reads and 2 writes**, and **26 Ozon reads and 4 writes**, including provider requests made by the worker and both order checks. All four automatic cycles recorded zero rate-limit responses. No additional provider requests were made after the read budget was exhausted. The ordinary product's existing mapping was recognized without recreation. Missing-row and UNKNOWN isolation were also exercised with deterministic multi-target fixtures; a live blocked target was not deliberately induced. Desktop and mobile browser views were inspected against the actual account data; deterministic browser tests used the real application, PostgreSQL, scheduler and worker with local provider transports.

The guided status is derived from existing registrations, imports, mapping records and the current policy/cycle rather than a second onboarding database. Suggestions are bounded per page and still require individual reviewed confirmation; selecting a group does not silently confirm it. Ozon product names are not part of the existing catalog import, so name-based lookup covers mapped or identifier-suggested MoySklad names rather than arbitrary Ozon names. Order checks are bounded and operator-initiated; they do not provide complete, continuously refreshed order coverage. The application remains a local-operator test integration with an exact-account publication exception, not an unrestricted merchant rollout.

## Single-owner merchant account verification (2026-10-29)

Migration 39 preserved the `cross-e2e` tenant identifier, its two encrypted
provider credentials, four product mappings and 20 existing publications. The
verified historical owner was explicitly bound to that account with the local
migration command. Its other, empty registration-created tenant and all audit
history remain intact; the existing encryption key was unchanged. A protected
local PostgreSQL backup was made before migration.

The owner signed in through the merchant UI without selecting an account.
Merchant-authenticated imports completed through the normal worker: MoySklad
operation `34916a78bc1ad3bbf362903484e03536` (batch 30, 5 attempts) and Ozon
operation `10ccc2caec2897dc487b4337c32193f8` (batch 34, 4 attempts). The
existing ordinary target began at source physical/reserve 0/0 and Ozon free 0.
The owner enabled an operator-attested policy for that single target via React
(revision 16, maximum 1); the first scheduled cycle was a write-free no-op.

| Source change | Automatic publication | Ozon result | Reconciliation |
|---|---|---|---|
| 0 → 1, MoySklad entry `9df967a1-d3c8-11f1-0a80-16cf00027307` | `d139ee8d31186e45b9ac06394eed86e7` | HTTP 200, `updated=true`, no errors | free 1, `RECONCILED` |
| 1 → 0, MoySklad loss `b26e4938-d3c8-11f1-0a80-1d6800025bb6` | `c9258b6efc24815b907dbaded0b98dfb` | HTTP 200, `updated=true`, no errors | free 0, `RECONCILED` |

Each publication had one outbox attempt. The merchant session is recorded in
the policy audit. Before each source document, an exact-target Ozon read
observed reserve 0 and the expected free stock. The bounded run used 28
MoySklad reads and two document writes, and 20 Ozon reads and two stock writes;
no rate-limit response was recorded. The automatic policy was disabled via
React at revision 17 after the final reconciliation. The prior superseded
UNKNOWN remains historical; no new publication outcome is unresolved. The
fixed-scope test mode is separate from the default-off production publication
policy, and these test observations do not establish production readiness.


## Engineering push: catalog, orders and dispatch fencing (2026-10-29)

**SUCCESS for the bounded read validation; no new live stock write was attempted.**
The merchant model remains one user per isolated account. Migrations 40–42
preserve existing identities, mappings, credentials and publication history.
The local database was backed up, and its credential-encryption key digest was
verified unchanged. Automatic synchronization remains disabled.

The normal credential-resolving worker completed merchant-authenticated Ozon
import `24878b2e77db09ada75683f9b6c38bb8`, linked to completed batch **38**:
**5 requests/5 attempts**, 33 products, 13 warehouses and 6 stock observations.
All 33 products received observed names through `/v3/product/info/list`.
Names are display/search metadata, never mapping evidence. The shared traversal
budget includes this extra request (the former four-request test cap must be
at least five for this account). Historical absent names remain unknown.
The exact ordinary target was freshly observed at Ozon free stock **0**.

A frozen seven-day order traversal completed through the merchant API:
**13 MoySklad attempts**, source batch **2**, six orders and seven positions;
**1 Ozon attempt**, posting batch **3**, no postings returned in that window.
The API returned HTTP 200; all recorded provider attempts completed successfully.
The completed progress and its original interval remained available after
browser reload. This run needed no continuation; larger-window continuation,
interruptions, conflicting repeats and partial-result exclusion were verified
against deterministic providers and isolated PostgreSQL schemas. Absence from
this bounded posting response does not prove absence of obligations. Aggregate
reserves and observed positions are not subtracted twice.

Order checks retain the 15-attempt per-provider bound per invocation. Completed
subintervals can be resumed with versioned stored credentials for up to 30 minutes,
with a 4 MiB staging bound per provider. Dense indivisible intervals remain
`LIMIT_REACHED`; environment credentials are deliberately single-shot. Checks
remain operator-initiated, not continuous complete order coverage.

Catalog matching now indexes confirmed mappings. On the documented deterministic
10,000-product fixture, median single-iteration evaluation changed from **2.31 s
to 9.47 ms**, while allocated bytes rose from **7.41 MB to 11.67 MB**. The warm
PostgreSQL target-name search changed from one **65.64 s** baseline run to a
**35.1 ms** median of three runs; its repeated source scan became one scan.
These are local synthetic measurements, not provider or synchronization latency.
Repeatable benchmarks and EXPLAIN instructions are included in the test files.

The dispatch fixes require a consumed, exact-target intent for every controlled
write, recheck authorization after rate-limit waits, and obtain a fresh database
snapshot after scope locks. Expired leases cannot revive after row-lock waits.
Order progress and dispatch share one credential-lock order; the regression
passes with that order and fails with the old order restored by a build overlay.
Migration 42 rechecks merchant session expiry under acquired actor locks and
retains that security correction on rollback. Historical superseded UNKNOWN is
shown separately from an active unknown result; its original outcome is unchanged.
PostgreSQL transaction-pooling proxies are unsupported by the session-lock path,
and no database fence guarantees ordering after an HTTP request has left STYK.

Desktop and mobile catalog search and persisted order-evidence screens were
inspected with the real account. The complete two-merchant browser test also
exercises registration, verified login, connections, imports, reviewed mappings,
inventory rules, scheduler/outbox/worker publication, reconciliation and durable
order checks through deterministic providers. The live read journal and local
screenshots remain under `.local/dev-run/`; credentials and raw provider payloads
are excluded from commits. Production publication remains disabled.

## V6 — reviewed bidirectional catalog synchronization (2026-10-29 UTC)

Implemented through the existing merchant API, PostgreSQL outbox and worker:
canonical field presence/provenance, versioned per-field ownership, frozen diffs,
approvals, creation markers, exact scoped dispatch, Ozon asynchronous receipts,
readback baselines, conflicts, no-op detection and explicit automatic field
allowlists. Stock calculation and stock publication remain separate. Migrations
43–44 preserve existing imports, credentials, mapping and publication history.
Eight implementation workstreams used three delegated agents and five isolated
Codex processes; their accepted commits were integrated into `master`.

**SUCCESS — real creation and updates.** The selected, previously unmapped Ozon
offer `BillYard1337` / product `2421418922` was active on fresh reads. Operation
`6855eafa3ee47f021b8f5063000dd3eb` created MoySklad product
`26c7eb21-d3db-11f1-0a80-1e640007bbe6`: HTTP 200, independent exact-ID readback,
`RECONCILED`. Its unique sync ID/external code and returned provider ID remain
separate. Repeating creation preview produced `CREATED_TARGET_REQUIRES_MAPPING`,
with no second creation. Normal durable imports completed on their first attempts:
MoySklad operation `97dea3c90d699633b57856b310d3dc4a`, new batch **39**, five
requests; Ozon operation `c51f67ec8e802b0d90a8d87c724cbdb0`, reused content batch
**38**, five fresh traversal requests. Normal reviewed mapping **5/v1** was
confirmed as `OPERATOR_ATTESTED`; existing mappings were retained.

A journaled, acknowledged MoySklad fixture-only PUT changed its name and set an
explicit STYK test description; no stock/document fields were changed. Through
the real React console, operation `7922491bd7cbd75f2e631fbfbebf9156` approved two
independent field directions: Ozon-owned name → MoySklad and MoySklad-owned
description → Ozon. Ozon accepted exactly one `/v1/product/attributes/update`
request (HTTP 200), task **5925189312**. Its final task response said `imported`
with two `warning_attribute_values_empty` diagnostics, severity `warning`, for
attributes **22232** and **23536**. Initial handling conservatively recorded
UNKNOWN and fenced the pending sibling; it did not resend.

The observed provider schema uses type **`String`**, not the fixture's lowercase
`string`; case normalization and a sanitized regression fixture corrected the
pre-dispatch validation mismatch. Poll receipts now retain actual HTTP status,
known diagnostic codes, severity and attribute IDs without raw provider messages.

**SUCCESS — audited read-only reconciliation.** The operator explicitly accepted
the warnings and requested a fresh task/selected-field check in React. Audit
`31c380b31999b74a3b09ed21c99a3c87` confirmed the exact description and recorded
`READBACK_MATCHED_WITH_WARNINGS`. The partial receipt and both warnings remain;
this does not certify unselected attributes or prove causality merely from a
matching observation. The original UNKNOWN attempt and complete failed outbox
row are unchanged (same recorded hash), and dispatch count remains **1**. A new
immutable readback request/result records the resolution. The previously approved
MoySklad name command then ran once through the normal worker, received HTTP 200
and reconciled. Both work items finished `RECONCILED`; no new unresolved external
write remains. The original historical stock UNKNOWN/recovery history is intact.

**SUCCESS — automatic no-op.** React enabled configuration revision **3**, with
only the mapped name/description allowlist and a 30-second interval. Four durable
scheduler previews were successful no-ops, with zero validation blockers and no
additional publication. React disabled automatic participation and catalog
synchronization in revision **4**. The first capture script needed to reopen the
collapsed settings after save; a separate normal UI session completed disabling.

Actual attempts within the announced fresh budget (≤200 reads/10 writes each):
**MoySklad 41 reads / 3 writes**, including five import reads and one direct,
journaled fixture-seed PUT; **Ozon 63 reads / 1 write**, including five import
reads and one extra read-only task diagnostic. All 92 instrumented catalog HTTP
responses and six direct fixture/diagnostic responses were HTTP 200; the ten
import attempts all completed successfully. There were no automatic write
retries. The normal stock publication count stayed **22**; no inventory movement
was created. Both stock and catalog automatic settings are disabled, and the API
and worker were restarted with publication flags off. The encryption-key digest
was unchanged; pre-migration database backups and local secret-free fixture
journals are retained under `.local/v6/`.

**Verification.** Real React/Go/PostgreSQL/worker tests cover mixed directions,
asynchronous reconciliation, explicit creation, duplicate prevention, tenant
attacks, no-op behavior and truthful pending-mapping states. The real merchant
screens were reviewed on desktop and mobile, including the warning resolution.
Manual readback PostgreSQL race tests preserve original commands, receipts,
UNKNOWN attempts and failed tasks, test concurrent claims/expiry/replay and reject
unsafe diagnostics and scope substitutions. Full Go lint is **0 uncapped** under
v2.15.0; `make lint`/CI now enforce that count. Final release commands and results
are recorded by the session's verification artifacts and delivery report.

A repeated local PostgreSQL selection benchmark (100 iterations, 50 returned
products, i5-13600KF) measured **0.855 ms / 0.652 ms / 0.959 ms** with
100 / 1,000 / 10,000 total products, approximately **83.3 KB** and **1,290**
allocations per page. This is a bounded local query measurement, not a claimed
before/after speedup or provider synchronization latency.

**Retained data / limitations.** The dedicated MoySklad product, confirmed mapping
and clearly identified test description are retained; physical inventory was
not changed. Ordinary catalog updates currently cover names/descriptions;
provider-specific creation DTOs support more factual fields, while unsupported
updates are explicitly blocked. Live creation of a new Ozon card is **NOT_TESTED**
because its mandatory factual declarations were not supplied or invented.
Variants, conflicts, partial failures and broader creation contracts have
deterministic coverage; this run does not claim live coverage for every category
or variant. Catalog enrollments are bounded to 100 per configuration. Large
provider traversals and distributed account-wide rate-limit coordination need
further scaling work. New merchants remain read-only until separately eligible;
this experiment establishes neither unrestricted publication nor production
readiness.
