# Controlled live orders and stock evidence

**SUCCESS for two historical order mirrors, three native service fields,
two attribute-only updates and the bounded FBS stock regression.**
Final verification: **2026-10-29 23:33:30 UTC** / 2026-10-30 Europe/Minsk.
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

Initial native discovery found one organization, three stores, five counterparties,
seven states and no custom attributes. The account initially had only default BYN;
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

## Native service fields and existing-order updates

The normal explicit metadata-setup API created exactly three native string fields:
`styk-order-id`, `styk-order-status` and `styk-order-substatus`. Each had one durable
dispatch authorization and one provider POST, all HTTP 200. The account registry
revision **3** contains their observed native UUIDs. The actual Orders UI selected
`commercial_order_id`, `status` and `substatus` mappings to those UUIDs and saved
them with PII disabled and no automation grant.

The first accepted creation initially remained UNKNOWN because its acknowledgement
could not be verified. A dedicated recovery endpoint reconciled its exact generated
marker using **four native GETs and no provider POST**. Its original UNKNOWN result
remains append-only history. The other two fields were confirmed by matching native
marker readback after unverified acknowledgements. Original native POST response
bodies were not retained; their exact shape and the initial mismatch cause are
not claimed as verified. No creation was replayed.

Fresh source discovery preceded explicit normal previews and separate approvals.
Both previews were Ready with **`owned_fields=["attribute"]` only**:

| Local update operation | Work | Provider HTTP | Dispatches | Final state |
|---|---|---:|---:|---|
| `65bf782a44ad88007e675c409d147653` | `176e8b61330cb5fd346a21f062b6393d` | 200 | 1 | RECONCILED |
| `b4becfaec487611b9687334a3cc6a2b9` | `da5da696205a5f1106590ee8863b31fe` | 200 | 1 | RECONCILED |

These were normal-worker PUTs to the **same two customerorders**. Worker readback
confirmed all three source values. A fresh two-target preview then returned Ready
with **zero commands for each**, independently confirming no remaining mapped
change. Stored non-PII references/scalars, complete positions and sums match the
pre-update snapshots. Native free text and attribute values remain redacted in
durable target observations; that mask is not evidence of a missing native value.
No foreign unmanaged custom attribute existed in this live case.

Both orders remain unposted with reserve zero. Existing source reservation-fixture
product `ea33e770-d389-11f1-0a80-0d480029c772` was physical **10**, reserved **3** in
normal imports **39** before the draft mirrors and **43** afterward. That pre-existing
reservation was preserved; no live active-posting reservation was created or claimed.
Final desktop/mobile inspection showed three verified field badges, selected
mappings, four reconciled operations, six native links and configuration disabled,
with no browser errors or overflow. Screenshots remain encrypted.

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
| MoySklad | **247** | **11** | **10**: currency, two drafts, three fields, two draft updates, entry, loss | 310 reads / 30 writes |
| Ozon | **83** | **2** | **2** selected stock updates | 150 reads / 20 writes |

Budgets include recovery reserves of **30 reads / 8 writes for MoySklad** and
**20 reads / 5 writes for Ozon**. The documented expansion covered explicit native
metadata setup, measured UI reference reads, stale-source refresh and full normal
update/readback/no-op checks. Counts
include the initial read-only phase (MoySklad 8, Ozon 11), five response-less
MoySklad transport attempts, one Ozon HTTP 400 read and the rejected MoySklad
HTTP 415 setup attempt. No provider 429 was observed. A legacy normal order-evidence
refresh made **five MoySklad and one Ozon read absent from the coordinator ledger**;
its returned attempt counters are counted separately here. The legacy coordination
fix was built afterward; no additional live request was used to claim its validation.
All other phase-two attempts were recorded by the shared PostgreSQL gate. Interim
status-zero snapshots included in-progress requests; the completed ledger contains
only the original five response-less attempts. All final permits are finished.

Requests were bounded, single-attempt, redirect-rejecting and paced. New fixture
mutations had exclusive durable local intents; normal mutations had application
intents/outbox records. No forbidden logistics-information endpoint, financial,
payment, shipment, refund or demand document was used. No new write remains UNKNOWN;
the recovered metadata creation's historical UNKNOWN result is retained, and
the original superseded historical stock UNKNOWN remains unchanged, without an
active UNKNOWN fence.

Migrations 45–47 and then index-only 48 were applied after encrypted mode-600
backups. Existing business/order row counts, credential ciphertexts and the
encryption key were preserved across the migrations. The v6 helper and journal
hashes remain unchanged. Order configuration revision **8**, manual stock sync,
automatic stock sync and catalog sync are disabled; no automation grant or active
outbox task remains. Temporary publication flags and schedulers were removed.
Latest normal APIs remain on loopback 8080/8082 with one combined worker for user
testing; port 8081's unrelated process was preserved. No provider secrets are in
their environment; the scoped Orders flag remains enabled with configuration off.
The live-phase API/worker binaries embed runtime commit `c8645a8`, including the reviewed
privacy-retention wiring; the dirty build marker reflects concurrent root-owned
browser-test edits. Targeted API/worker builds passed. The final clean release
build and exact source revision are recorded separately in the private runtime
manifest and final delivery; these live-phase observations are not rewritten.

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
The metadata setup contract uses the official
[general source](https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/_general.md),
retrieved and verified by the implementation agent on **2026-10-30**, byte SHA-256
`5dc61a23b376b7b2ea46ece7ac6f33f56476d2c34fdf152d457cbeb8e0da0c16`.
This source verification does not establish the unretained live POST response shape.

Ozon returned 13 warehouses and 10 delivery methods, with separate identities.
No actionable live FBS/rFBS posting was observed. Packing, labels, tracking and
shipment/delivery actions are **NOT_OBSERVED_LIVE**; rFBS stock writes are
**NOT_TESTED**. This test does not establish general production readiness.

Private handoff: `.local/orders-live/final-normal-evidence.json`, its encrypted
full counterpart, `stock-regression-journal.json`, `phase2-journal.json`,
`final-metadata-evidence.json`, `source-evidence.json`, `mirror-inputs.json`,
encrypted payloads/screenshots and
backups. Sensitive customer values and native names remain encrypted or transient;
safe identifiers and counts are separate. The legitimate owner's reset password
is only in the designated private file.

### 2026-11-04: deterministic split reconciliation follow-up

Split receipts now retain returned posting identities and bounded, resumable
child observations. Reconciliation checks the exact approved packages, quantities,
commercial order, account, warehouse and delivery method. PostgreSQL independently
checks the immutable preview and keeps observations in append-only receipt history.
These are serial detail reads after acknowledgment, not an atomic provider snapshot.
Pending children are re-read; UNKNOWN never becomes a write retry. Child observations
here are reconciliation evidence, not a claim that native child reservations have
already been projected or that a shared inventory frontier has been transferred.

Verified locally: complete Go race suite, make lint/test/build, default and
integration-tag golangci-lint (0 findings), PostgreSQL split/store/ingestion
regressions, frontend typecheck/lint/build, 234 frontend tests and five synthetic
browser journeys. Desktop/mobile split screenshots inspected. No new live provider
requests occurred in this follow-up. Live split remains NOT_OBSERVED_LIVE.
The legacy live-order diagnostic is explicitly opt-in and does not qualify as
normal-pipeline E2E evidence because it still contains direct-provider/SQL fallbacks.

### 2026-11-04: protected label downloads and projection regression fixes

Implemented the reviewed official label-create v3 / task-get v2 response shapes:
create sends posting numbers only, retains both returned format/task identities,
and polls the selected task without replaying creation. Missing print counts stay
unknown. Acknowledgment is not reconciliation: one requested and printed posting,
no target errors, a validated PDF, and atomic encrypted artifact persistence are
required. Download requires a current merchant session and exact account ownership.
Artifacts expire within 24 hours, or the shorter configured PII retention; cleanup
also runs when automation is disabled. Open browser pages expire their links locally.

Task-authorized CDN URLs are transient, exact and single-use. Public DNS addresses
are checked and pinned, TLS retains the original hostname, redirects/proxies/client
credentials are excluded, and the download shares the PostgreSQL account budget.
File paths, signed URLs, provider free-text errors and PDF contents are excluded
from ordinary receipts. Contract subset: `internal/orderactions/label_contract.json`,
reviewed against the [official Swagger](https://docs.ozon.ru/api/seller/zh/swagger.json).
This is an explicit subset, not a claim of complete fulfillment schema coverage.

Migration 52 adds encrypted artifacts with scoped foreign keys and immutable rows;
rollback refuses to delete retained artifacts. The historical local database was
backed up with the existing encryption key before upgrading 48 to 52. Comparisons
of original columns/rows in 114 tables preserved historical data; the encryption
key was retained. Stock, catalog and order automation remained disabled.

Verification: complete prepared PostgreSQL/race run, **630 passed test events,
64 skipped, zero failures**. Skips retain their original reasons; supplemental
stock regression run had **60 passes / 2 skips**, and the nine-process browser
follow-up **12 passes / zero skips**. These counts include subtests and are not
additive unique-test totals. Nine-process testing uses 3 APIs, 2 schedulers,
4 workers, one PostgreSQL and deterministic providers. Label-specific PostgreSQL
checks cover atomicity, wrong-account rejection, shorter retention, session
revocation, HTTP access by two merchants, and non-destructive rollback.
Six synthetic order-console browser journeys pass, including actual downloaded
PDF byte comparison and clock-driven expiry; desktop/mobile screenshots reviewed.
Those browser label routes are simulated; they are not live Ozon evidence.

A separate service-preview regression exposed and fixed customer attribute
allowlist collisions: valid protected customer mappings no longer fail their own
validation, while duplicate/unapproved fields remain rejected. No new live
fulfillment commands or real buyer/delivery events occurred in these checks.
Live labels and delivery transitions remain **NOT_OBSERVED_LIVE**; native demand,
salesreturn and safe structural line replacement are not established by this work.

### 2026-11-04: owned native line updates and integration checkpoint

The normal order pipeline now supports one reviewed update of an existing native
position UUID: quantity, price and owned reserve, with exact prior successful
source/native binding. A complete fresh native frontier is frozen and rechecked;
foreign lines/reserves are preserved. Quantity/reserve changes require known zero
shipped quantity; price changes require known zero discount and the same currency.
Documented native surcharge discounts remain readable. New/deleted lines,
assortment changes and tax changes remain separate unsupported mutations.
A reconciled line patch does not assert that every field or other line is current.

Real Go service preview, merchant HTTP approval/CSRF/idempotency, PostgreSQL,
outbox, normal runner and the actual MoySklad adapter were tested with deterministic
HTTP providers: one position PUT changed quantity to 1 and price to 19.99 RUB;
foreign quantity 7/reserve 2 remained unchanged. Independent readback reconciled;
subsequent preview was a no-op. Source/manual-target changes rejected dispatch.
A lost response remained UNKNOWN, then a new runner reconciled by reads without
another PUT. Additional regressions fixed large int64 revision rounding and
customer-consent/fresh-private-value checks; the unused unjournaled status writer
was removed.

Hybrid handover requires a reconciled same-posting label, unchanged source
revision, matching warehouse/method, unexpired encrypted PDF and explicit operator
print confirmation. Preview, approval and final dispatch revalidate the proof.
The merchant selects that label; expiry clears the selection without polling.
Eight typed Ozon packing requirement lists distinguish missing/null from explicit
empty. Outstanding SKU-specific legal/exemplar requirements are actionable
blockers, never invented values or inline additions to the ship-v4 payload.
Separate exemplar write operations were not implemented in this checkpoint.

Migration 53 preserves native position provenance and refuses destructive rollback
with position-update history. Migration 54 serializes active Ozon posting actions
across different MoySklad projections; it retains UNKNOWN fences. The historical
local upgrade 52→54 preserved all **4,410 rows in 117 existing tables** and the
original encryption key, after another encrypted custom-format backup. No existing
publication outcome was rewritten.

Verification at this checkpoint: make lint/test/build, complete Go race suite and
full integration-tag lint (0 findings). Prepared full PostgreSQL/race run:
**653 passed / 64 skipped / 0 failures**. Supplemental dedicated stock suite:
**60 passed / 2 skipped**; nine-process/browser gate: **12 passed / 0 skipped**.
Counts include subtests; detailed skip reasons and supplemental matches are in
`orders-test-skips.json`. The process gate uses 3 API processes, 2 schedulers,
4 workers, one PostgreSQL and two merchants. Frontend typecheck/lint/build,
**235 unit tests** and **8 synthetic browser journeys** pass; desktop/mobile
screenshots inspected, including current and expired hybrid label selection.
Subsequent focused race tests verified independent fresh-target evaluation despite
partial collection failure, without approving stale/UNKNOWN targets or losing the
read error. No stock formula or partial-traversal completeness was changed.

Live continuation through two authenticated normal APIs and two normal workers:
**8 MoySklad metadata reads, all HTTP 200**. One Ozon order-read attempt ended
without an HTTP response; a separate unauthenticated TLS connectivity probe also
timed out. This does not establish an account permission rejection. The active
local traversal checkpoint advanced without claiming fresh provider evidence for
the two cached historical cancelled postings. No provider writes were dispatched;
order work count remained unchanged. Temporary read configuration revisions 9/10
ended disabled with no automation grant. Normal merchant desktop/mobile UI was
checked against the historical database without API mocks; sessions worked across
both API processes. Existing general publication restrictions remain active.

This checkpoint does not establish live ship/delivery/marking behavior or native
shipment/return processing. Native movement, exemplar and operator recovery work
continues on isolated implementation branches and is not credited as integrated
functionality here.

### 2026-11-04: integrated continuation through migration 58

This supersedes the earlier checkpoint's statement that native movements,
exemplar operations and recovery were still on unintegrated branches. They now
use the normal merchant preview, approval, outbox and worker:

- Separate draft/posted MoySklad demand and linked salesreturn intents. Posted
  documents need an expiring explicit policy and a factual warehouse event; Ozon
  status is not shipment/return evidence. Independent document and parent-counter
  readback are required. A verified native document can be displayed without
  rewriting an original lost-acknowledgment receipt as successful.
- Typed exemplar preparation/set operations, durable asynchronous status checks,
  explicit optional-field clear semantics and expiring merchant review. Unowned
  weight/multibox metadata is not erased by an incomplete set request.
- Country dictionary/set operations use SKU identity despite the provider's
  `product_id` parameter name. Acknowledgment plus removal of the requirement is
  not observation of the stored country value; the UI states that limitation.
- Audited operator supersession of UNKNOWN updates to a known customerorder.
  The original work and receipt remain UNKNOWN. Recovery authorizes a separate
  fresh review, never replay or automatic compensation.
- Structured native shipment-address fields preserve independent existing
  country/region references. Buyer identity remains separate from accounting
  counterparty; protected values are not serialized in previews/audit payloads.
- Retained delivered/cancelled postings are checked every six hours within the
  configured retained horizon, in addition to active postings. One failed detail
  read no longer discards successful independent observations or falsely marks
  partial coverage complete. Missing status is not borrowed from older evidence.

The 54→57 and 57→58 local upgrades preserved all pre-existing table columns and
rows (117 tables/4,410 rows and 127 tables/4,433 rows respectively), credentials,
the original encryption key and publication history, following encrypted backups.
The prepared full PostgreSQL/race checkpoint at migration 57 passed **682 test
events / 64 skipped / 0 failed**; it predates later focused country, attribution
and ingestion fixes and must not be described as final-HEAD full verification.
Those changes have their own focused HTTP/PostgreSQL/race regressions.

Fresh normal imports on the retained real accounts completed through two API
processes and two workers: MoySklad operation `da693213a01bde1a15ffbc9f5d7810f3`
and Ozon operation `0b8ce8865133cfefe041188fccedd203`, five HTTP reads each. Both reused immutable content
batches while recording new successful database-admitted traversals. This does
not refresh historical source timestamps or prove an atomic provider snapshot.
The earlier bounded read continuation also returned HTTP 200 for one Ozon
unfulfilled request and eight MoySklad metadata requests, without writes.

Live native shipment/return events and live fulfillment transitions are not
claimed here: no actionable buyer event is fabricated to obtain coverage. The
regular rFBS controlled stock transition is tracked separately and is not counted
as successful before normal-worker acknowledgment and readback. General
production publication remains restricted.

### Completed regular rFBS and Express stock validation

Both controlled targets used the existing zero product mapping **4/v1**:
MoySklad `e8f82883-d389-11f1-0a80-1d6400282a1d` → Ozon
`3531908963` / `ATEST-897900` / SKU `3468089643`. Warehouse mappings were
confirmed through the application as OPERATOR_ATTESTED; source physical pools
were distinct. No existing FBS mapping or history was replaced.

| Profile | MoySklad store | Ozon warehouse | Normal publication operations (0 → 1 → 0) | Final result |
| --- | --- | --- | --- | --- |
| Regular rFBS, mapping 2/v1 | `296ef417-d30a-11f1-0a80-18aa0000de3e` | `1020001344839000` | `1e93e737d545d58dd7e616ca30cf550e`, `231cf7511a21b531db3178197dc3051c`, `d482c37143aa28f734cfcb93c1ef6d5b` | Three RECONCILED |
| Express, mapping 3/v1 | `7bffba7a-d31c-11f1-0a80-10b20006b039` | `1020001795665000` | `682837a9c938870221890b31f6111c58`, `b0762d1b5ba57b4b2aad449d92247588`, `66d3a36b1c51e91c9ec62eb8c5a867af` | Three RECONCILED |

All six writes received HTTP 200, `updated=true`, no per-target errors and
independent readback. Four real stock-entry/loss documents established and
restored the two source quantities; all were independently read back. Both
marketplace warehouses ended present/reserved/free **0/0/0**, source stock/reserve
**0/0**. Final Ozon import `e87a7d240fb7b7eba15104487f2b67de` completed normally.
The provider interval was checked using the database clock before each decision.
Regular cycles stopped by the deliberate configuration change are not reported
as COMPLETE; Express positive/restoration cycles completed before disabling.

The two stock phases made Ozon **88 reads / 6 writes**, MoySklad **66 reads /
4 writes**, all 164 responses HTTP 200. Private journals are
`.local/orders-v8/rfbs-live-20261104.json` and
`.local/orders-v8/express-live-20261104.json`. A refused local source-entry intent
made no POST and remains recorded; its exact-reference parser was corrected for
the official `expand=supplier` query suffix before a new intent was created.
Two historical cancelled postings were also freshly reread; no ship, tracking,
label, cancel, payment or genuine delivery action was invented.

### Real native draft, UNKNOWN and read-only recovery

A separate manual draft-only policy admitted one known mapped line, quantity 1,
150 RUB, actual observed zero line VAT and an unposted demand. Operation
`8370acb04fa7b6b32657891dd5541ac6`, work `702739b87ff61869dbbec4c684cb06a0`
made exactly **one** normal-worker POST. HTTP 200 could not initially be verified;
the original receipt remains UNKNOWN / HTTP 200 with no fabricated external ID.
A reproducible explicit-gzip response decoding defect was fixed and tested; the
original response encoding was not retained, so that cause is not retrospectively
asserted as proven. No second live POST was sent to validate the parser.

Exact syncId/marker discovery found demand
`f3bf819b-d85d-11f1-0a80-10b9007810c7`, `applicable=false`. Normal readback then
exposed the documented minute precision of `moment`: the immutable intention
15:41:17.584Z was observed as 15:41:00Z. The official DateTime rule is recorded in
`moysklad-accounting-moment-contract.json`; precise source/event timestamps remain
unchanged. Pausing no longer prevents authorized reads of an already dispatched
request, while new writes still require the current policy.

The corrected normal worker reconciled native document **and parent** with code
`NATIVE_DOCUMENT_MOMENT_MINUTE_CONFIRMED`, preserving the original command
fingerprint, receipt and UNKNOWN history. A separate reconciliation records the
actual document UUID and observed minute. Quantity, price, reservation and shipped
quantity were 1 / 150 RUB / 0 / 0. The unposted fixture is retained. Native phase:
Ozon **9 reads / 0 writes**, MoySklad **43 reads / 1 write**, all 53 HTTP 200.
Read budgets were explicitly revised 30→40→50 to diagnose and reconcile without
resending. Failed readback workers were stopped before another observation phase.
The private journal is `.local/orders-v8/native-draft-live59.json`.

All temporary automation is OFF: stock 29, catalog 4, orders 18, no active grants
or native posted permission. Runtime stock/catalog publication flags are unset.
All new external operations are reconciled; historical UNKNOWN receipts remain.
The 58→59 local migration preserved all original columns/5,462 rows in 127 tables
and the encryption key, following another encrypted backup.

Merchant browser verification used the real React application, both APIs and the
historical database, without API mocks. Login/history and disabled configuration
worked on desktop/mobile. GUI metadata checks made 33 MoySklad reads and no writes:
the first 12-request estimate was exceeded at 17 and recorded explicitly; the
second 16 stayed within a separately approved 20-request budget. Completed metadata
loading was not asserted from those screenshots. Deterministic tests prove ordinary
cache reuse and cancellation; two complete development traversals do not establish
a production cache defect.

### Integrated quality gate

- Full PostgreSQL/integration race checkpoint (compiled at `8ec67ea`, schema 59):
  **728 PASS / 66 SKIP / 0 FAIL**, 1,608 seconds. Subsequent accounting-minute
  changes have the separate current native/startup/process race DELTA:
  **19 PASS / 0 SKIP / 0 FAIL**, 201 seconds, including immutable legacy UNKNOWN.
- Dedicated stock database supplement: **112 PASS / 6 intentional SKIP / 0 FAIL**.
  All 51 primary cases requiring that database were actually run successfully.
- Current React → Go → PostgreSQL → normal worker browser/process stack:
  **12 test/subtest PASS / 0 SKIP**, 96 seconds; 3 API, 2 scheduler, 4 worker
  OS processes and two independent merchants. Test children were stopped.
- Frontend typecheck/lint/build and **276 unit tests** pass; **14 separate
  console browser journeys** pass, with desktop/mobile screenshots inspected.
- Current make lint/test, complete Go race suite, go vet and builds pass; full
  integration-tag golangci-lint also reports **0 findings**. The existing large
  frontend chunk warning remains visible; no lint rule or baseline was weakened.

`orders-test-skips.json` retains primary skips and links actual supplements.
The earlier schema-58 attempt (**698 PASS / 8 FAIL / 66 SKIP**) remains recorded
as failed: stricter ownership exposed synthetic creators without dispatch proof,
and a rollback test targeted the newest migration instead of recovery migration
57. Fixtures now use actual durable dispatch and rollback targets are explicit.
Skipped paid/live-event and legacy diagnostics were not turned into synthetic PASS.

New composition support is manual, reviewed, all-owned and unshipped, with known
zero VAT and supported plain native rows. Foreign/manual/unknown fields block the
affected replacement. Composition UNKNOWN does not adopt new UUIDs or replay.
Order reserves use an explicit versioned pending-quantity rule; stock baseline
attribution remains independently proven, not assumed from the reserve write.
Exemplar updates can preserve freshly observed weights and grouping without unit
conversion or new measurements. Real ship/delivery/marking and posted native
shipment/return events remain NOT_OBSERVED_LIVE because no applicable genuine event
was fabricated; supported paths have deterministic HTTP/worker evidence.
