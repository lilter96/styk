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


### Continuation: observed included VAT and return-boundary validation

Native demand/return plans now preserve a nonzero integer VAT percentage already
observed in MoySklad when document VAT is enabled and included in the gross
price. They do not infer a seller tax declaration or treat an Ozon fractional
rate as a native percentage. The gross amount remains exact quantity × observed
price; VAT is not added again. Discount-dependent, excluded-tax and fractional
minor-unit rounding remain explicit blockers. Existing zero-tax behavior remains.

Contract checked 2026-11-04: the official
[demand specification](https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/documents/_demand.md)
defines document `vatIncluded` as inclusion in price, position `vat` as an integer,
and position `vatEnabled` separately from exempt/zero treatment. Return tax
headers and line financial values are inherited from the exact demand basis,
not from a later edited customer order. This does not change the interpretation
or mapping of Ozon posting VAT fields.

Adversarial review found a pre-existing return-boundary gap: the planner copied
basis fiscal values, but command validation did not independently bind outgoing
return price/currency/discount/VAT to the exact demand position. The domain now
checks the basis UUID, item identity, fiscal values and duplicate basis use.
Payload monetary validation independently rechecks complete prior-return evidence
and remaining quantity using exact decimals; only posted returns consume physical
return quantity. Changed basis facts still fail the existing fresh preflight.

The normal merchant HTTP → PostgreSQL → outbox/worker → deterministic MoySklad
transport exercises a draft, posted demand and linked return with observed 20%
included VAT, retaining the original 12.34 RUB gross unit price. No live tax,
physical shipment or return was created for this extension. The first test setup
reused a physical-event correlation reference for two intents and correctly got
HTTP 409; distinct event references corrected the test rather than weakening
intent uniqueness. The earlier failed attempt is not counted as passed.

Verification for this extension: `make lint test build`, complete Go race suite,
Go vet and integration-tag golangci-lint pass (0 findings). Four selected normal
native HTTP/PostgreSQL scenarios, including lost acknowledgement and stale-source/
foreign-reserve guards, pass with race instrumentation in 79.676 seconds.
Frontend typecheck/lint/build, 276 unit tests and 14 orders-console browser
journeys pass. No schema migration or live provider request was required.
The legacy default `npm run test:browser` smoke failed because it expects the
removed local-operator “Ключ входа” form; this is retained as a failing legacy
harness, not reported as a successful merchant-authentication E2E. The explicit
current orders-console suite passes separately.

The official [sales-return contract](https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/documents/_sales_return.md)
was also checked for VAT inclusion, enabled state and the demand-basis link.
Live included-tax shipment/return remains NOT_OBSERVED_LIVE. This extension does
not claim that all fiscal rounding cases or the complete release are finished.

Local runtime was then rebuilt from clean source `e0804b2` and the four owned
processes (two APIs, two normal workers) were restarted after database checks
showed zero enabled order/stock/catalog configurations, active provider calls,
dispatches and pending/running outbox tasks. Both API readiness checks passed.
General stock/catalog publication flags remain unset; no live provider mutation
was requested by this continuation. The unrelated `AGENTS.md` edit is preserved.


### Merchant-authenticated browser entry and interruption recovery

The previously failing default `npm run test:browser` no longer targets the
removed local-operator secret form. It starts the existing isolated nine-process
orders harness, requires an explicit dedicated localhost `TEST_DATABASE_URL`,
strips real provider credentials/publication flags and invokes both current
merchant smoke and order-publication browser scenarios. Effective PostgreSQL
host/database/fallback validation remains in Go; `.env` and historical credentials
are not loaded. No second scheduler, queue or production authentication bypass
was introduced.

The smoke registers a new merchant through React, waits beyond the 15-second
session refresh while typing, verifies email delivered through the actual test
SMTP path, logs in through round-robin API replicas, reloads and logs out.
A separate random-secret mailbox route exists only in the integration harness
and only serves the exact synthetic registration email. The normal API returns
404 for that route. The second browser journey still performs merchant review →
durable operation → outbox/worker → provider customerorder → readback and checks
another merchant cannot read the order resources. No API response is mocked in
these stack browser scenarios.

Final normal invocation: **12 Go tests/subtests PASS, 0 SKIP, 0 FAIL** in
119.809 seconds; its two Playwright scenarios pass in 33.4 seconds. The stack
uses 3 API, 2 scheduler and 4 worker OS processes, one PostgreSQL and independent
merchant sessions. It observed 778 deterministic loopback-provider HTTP attempts,
minimum configured account gap 41.46 ms, maximum in-flight count 1. These are
synthetic provider measurements, not Ozon throughput or live order evidence.
The existing lost-response, stale-worker, credential-rotation and four kill/
restart phases all remain in the default gate.

Adversarial review found that forwarding a signal only to the Go driver could
leave replicas alive. The POSIX runner now owns a separate process group and
terminates that group on interruption. A real SIGTERM experiment verified
**11 descendants and zero remaining running descendants**, with an expected
nonzero runner exit. The initial Linux inspection was corrected to enumerate
children of every Go OS thread, rather than only the main thread. No live fault
injection or provider mutation occurred.

Desktop and 390-pixel mobile screenshots were inspected; no horizontal overflow.
`make lint test build`, full Go race tests, integration-tag golangci-lint (0
findings), frontend typecheck/lint, 276 unit tests and production build pass.
The pre-existing large frontend chunk warning remains. All test processes/Vite
were stopped; the normal application remains running with temporary automation
OFF. No migration or historical record change was needed. Private evidence:
`.local/orders-merchant-smoke-final-stack.log`,
`.local/orders-merchant-smoke-interrupt.json` and
`.local/orders-merchant-smoke-screenshots/`.

### Fulfillment readbacks, cancellation and staged split reserves — 2026-11-04

Verified fulfillment detail reads now enter the ordinary source-observation
pipeline in the same transaction as reconciliation. Their original check times
are retained. A late unversioned response remains historical evidence when a
newer source head or an active collector prevents serial admission; allocating a
new local sequence does not make an old provider read current. Invalid mixed
split readbacks roll back atomically. Partial-cancellation proof must follow the
actual dispatch attempt, not merely the earlier review.

Deterministic merchant HTTP → PostgreSQL → normal outbox/worker tests verify:

- Partial cancellation 2→1 updates the owned native quantity/reserve without an
  intervening manual import. Lost Ozon and MoySklad responses do not replay either
  mutation; an independent native reserve of 2 remains unchanged.
- A confirmed split before any native parent projection creates two separate
  package orders with reserve 1 each, rather than the obsolete whole parent.
  Repeated reviews are write-free. Child identity comes from the actual local
  split receipt, without inventing provider ancestry.
- An existing retained parent may reduce only its independently owned,
  unshipped quantity/reserve. The permission binds the frozen original review,
  native correlation, split work and current ownership frontier. Changed prices,
  VAT, mappings, growing quantities, attested ownership and earlier ancestry
  cannot use this reduction permission.
- Children of an already reserved parent remain blocked, including after the
  reduction. Complete transfer authorization still needs a separate fresh,
  scoped dependency proof; this checkpoint does **not** claim it is implemented.

Existing native bindings established by read-only reconciliation are not treated
as absent merely because no POST was recorded. Shortened provider responses do
not erase ancestry retained in the reviewed source. Nested splits remain fenced
behind their ancestor obligations. These are protections, not completed support
for every nested or fully superseded-parent transfer.

The related earlier checkpoint passed 157 PostgreSQL/integration tests with six
explicit skips; its later current delta passed 17 tests without skips. The
retained-parent extension and existing split/ancestry/binding cases subsequently
passed with race instrumentation in 68.101 seconds. The separate nine-process
native gate passed in 58.501 seconds, and the ordinary merchant/browser gate
passed in 120.113 seconds. Those runs use deterministic providers and do not
constitute live fulfillment writes. Test helper/opt-in/live-credential skips
remain distinct from the separately executed opt-in gates.

A fresh **read-only live** check used the existing merchant API, scheduler,
workers and account-wide budgets: **6 Ozon requests, all HTTP 200; MoySklad 0;
external writes 0**. It comprised one unfulfilled list, one creation-date list,
two exact posting reads, warehouse discovery and delivery-method discovery.
The cutoff window 2026-10-04T17:30:40Z–2026-11-04T17:30:40Z was empty; the
creation window 2026-10-04T17:38:59Z–2026-11-04T17:38:59Z contained the two known
cancelled FBS postings, with no available actions. No suitable active posting
was observed within these bounded windows. Future cutoffs and orders outside
these windows are not proven absent. No buyer event, delivery or shipment was
fabricated to obtain a live transition.

Temporary read configuration was restored to its original disabled contents at
audited revision 22, without an automation grant. No new live work or mutation
intent was created. The key, encrypted credentials and historical UNKNOWN
receipts remain unchanged. Schema remains 59. Private request evidence is in
`.local/orders-v9/active-discovery-20261104.json`.

After the retained-parent changes were integrated, the final focused PostgreSQL
race run passed **21 tests/subtests, 0 skips, 0 failures** in 119.299 seconds.
The current native nine-process gate passed in 59.042 seconds; the normal
merchant/order browser stack passed in 124.889 seconds with two Playwright
journeys. That deterministic stack recorded 772 loopback-provider attempts,
minimum account gap 42.04 ms and maximum in-flight count 1. These measurements
do not describe live Ozon throughput. Current desktop and mobile screenshots
were inspected. `make lint test build`, full Go race tests, integration-tag lint
(0 findings), frontend typecheck/lint, all 276 frontend unit tests and production
build pass. The existing frontend chunk-size warning remains.

The normal local runtime was rebuilt from clean commit `91e2ca9` and its four
owned processes were replaced only after a zero-active-work/provider-call check.
Both API replicas passed liveness/readiness. Schema 59 and historical evidence
remain intact; temporary automation and general stock/catalog publication stay
disabled. The implemented retained-parent reduction is a useful first stage,
not authorization to double-reserve children or a claim that the complete order
release has passed every production gate.

### Retained split transfer and automatic native evidence — 2026-11-04

The retained-parent limitation above is superseded for one independently proven
split level. After an acknowledged, reconciled reduction of the existing
parent's owned reserve, an immutable child permission binds the split receipt,
parent release, current native identity, complete positions and mapping scope.
The normal worker rereads both the parent posting and native order before child
creation. Changed parent facts, foreign reserve changes, an active dependent
child and an unresolved parent response remain fences. Parent/child dependencies
are serialized in PostgreSQL. UNKNOWN history is not rewritten or replayed.
Nested chains and a fully superseded parent that cannot be read remain gaps.

The worker now refreshes exact bound MoySklad customerorders through the existing
leased order collector, including terminal postings. Migration 60 stores immutable,
PII-free point snapshots separately from actual read attempts and their stable
DB admission clocks. These reads never claim complete account/window coverage or
create `moy_order_batches`. New provider facts or a later failed read invalidate
older proof. An unchanged snapshot may reuse an eligible pre-baseline attempt,
so periodic polling does not continuously invalidate a long stock traversal.

Attribution requires a point admitted after reconciliation, recorded before a
new durable stock traversal, and still inside the configured evidence lifetime.
A fresh aggregate reserve cannot refresh stale order attribution. Missing,
null, explicit zero and deleted native documents remain distinct. A cached
cancelled-order zero cannot bypass a newer native reserve or failed read.
Every actual ingestion HTTP attempt revalidates its lease and current recorded
merchant permission after shared budget admission; logout alone does not revoke
a durable permission. Startup requires schema 60 and its actual structures.

Deterministic validation on this checkpoint includes:

- Normal HTTP adapters → PostgreSQL collector → durable stock import → attribution:
  **21.343 seconds**, zero full-account native-order batches. A partial shipment
  remains blocked; a fully observed shipment settles only after fresh native
  evidence and a subsequent source traversal.
- PostgreSQL point storage/authority/migration regressions: **25.877 seconds**;
  actual eligible-snapshot batch-query regressions: **3.678 seconds**. Clock skew,
  later failures, credential rotation and tenant/account substitution are covered.
- Normal nine-process browser gate: **126.806 seconds**, all 12 Go tests/subtests
  and both Playwright journeys passed. It recorded 1,018 loopback HTTP attempts,
  minimum measured account arrival gap 18.418536 ms and maximum in-flight 1.
  These are synthetic observations, not provider throughput or a new rate policy.
- Native nine-process plus startup/migration gate: **68.942 seconds**, passed.
  Desktop and 390-pixel mobile merchant screenshots were inspected. No real
  buyer data is included in these screenshots.
- `make lint test build`, full Go race, default/integration-tag lint (zero),
  frontend typecheck/lint, all **276 frontend tests** and build passed. The
  existing 716.36 kB frontend chunk warning remains.

The broader current related PostgreSQL regression run is tracked separately;
its result is not inferred from these focused gates. Earlier complete related
checkpoint `c3c6f3c` passed 176 test/subtest events with six explicit opt-in/helper/
live-key skips. Fulfillment writes are not considered live-validated merely
because these deterministic tests pass.

The bounded **live read-only** native-point gate then passed through the normal
merchant API, scheduler, two API replicas and two workers at clean `1b4d896`.
It made **14 MoySklad GETs and 5 Ozon reads, all HTTP 200; external writes 0**.
Exactly two known native orders received successful point snapshots/read records:
quantity 1, explicit reserve 0 and shipped 0, with verified pack/unit semantics.
Credential revision 2, actual read clocks and DB admission/recording were retained.
No full-account native-order batch, new order work or mutation intent was created.
No new source-stock traversal was requested in this live gate; these point reads
alone are not claimed to settle a stock baseline.

Development migration 59→60 preserved all original columns/row fingerprints in
127 tables (5,669 rows), credentials and the encryption key. An encrypted backup
was taken first. Four owned API/worker processes now run clean `1b4d896`; both API
replicas pass readiness/liveness. The temporary read configuration 24→25→26 was
restored to its exact original disabled contents: test/native/automation OFF,
stock/catalog publication flags OFF, zero active calls, pending work and grants.
Private evidence: `.local/orders-v9/native-point-read-20261104.json` and
`.local/orders-v9/upgrade60-result.json`. No paid buyer event, physical shipment,
new external write or UNKNOWN replay was performed in this continuation.

### Assembly references and completed native-point regression gate — 2026-11-04

The related PostgreSQL race gate at the native-point checkpoint completed:
**218 passing test/subtest events, 6 explicit skips, 0 failures**; integration
package elapsed time 1,170.572 seconds. Skips were the legacy live opt-in,
two parent-invoked subprocess entrypoints, the native process opt-in, browser
opt-in and the live Ozon-key test. The native and browser opt-in gates were also
executed separately; a primary skip is not rewritten as a primary pass.

Official reviewed detail fragments now support typed `related_postings` reads.
Only `/v3/posting/fbs/get` requests the documented flag; list requests do not
receive an unverified extra parameter. Optional container/member presence and
explicit empty arrays are retained. These are assembly references, not parent/
child ancestry, a complete split result or proof that another posting vanished.
The immutable source retains them; shortened projections retain prior known
facts, and reserve preflight still validates actual allocation/status/native
frontiers rather than treating assembly metadata as allocation authority.

The merchant can inspect assembly references and search an associated posting;
initial large-list rendering is bounded. The updated real-process merchant
browser gate passed in **136.783 seconds**, including both Playwright journeys,
linked-posting navigation, confirmation, worker reconciliation, isolation and
mobile overflow checks. Desktop and mobile source-card screenshots were inspected.
It measured 1,083 loopback HTTP attempts, minimum observed arrival gap 18.488752 ms
and maximum in-flight 1; these are synthetic observations. Related transfer
PostgreSQL regressions also passed in **57.813 seconds**. Two new browser locator
assertions were corrected after failed attempts; the failures were retained.
The earlier alternate test-database attempt did not start a process stack.

A bounded normal **live read-only** check used two complementary collector cycles:
**6 Ozon reads, all HTTP 200; MoySklad reads 0, external writes 0**. Both known
cancelled postings returned and persisted a present assembly group with an
explicitly empty member list. No live nonempty member element, retired parent,
ship/delivery transition or transferred reserve is claimed by this check.
Private ledger: `.local/orders-v10/related-read-20261104.json`.
Configuration 26→27→28 was restored to its exact original disabled contents.
Clean runtime `cdd1861`, schema 60, both API replicas healthy, no pending work,
active provider calls or automation grants; all general publication flags OFF.
No credentials, key or historical UNKNOWN record changed. Current Go lint is 0;
make lint/test/build, full race, frontend checks and **283 frontend tests** pass.
The frontend retains its existing large-chunk warning (717.65 kB main chunk).

The isolated superseded-parent candidate was not accepted: adversarial tests
found deletion, source-proof mutation, drift and malformed-field defects. Its
accidental master application `7ddc857` was immediately undone by ordinary revert
`7cb3e04`; neither was deployed. Production dispatch for that unfinished transfer
remains disabled pending corrected source proofs and full normal-pipeline wiring.

### Real reservation/stock automation and admission recovery — 2026-11-04/05

The owner-authorized ordinary fixture was exercised through the normal merchant
API, automatic scheduler, durable outbox, two workers and independent provider
reads. MoySklad physical/reserved observations were **0/0 → 2/0 → 2/1 → 2/0 →
0/0**. Four distinct normal publications reached Ozon **2 → 1 → 2 → 0**:

| Publication | Intended/readback | Actual result |
| --- | --- | --- |
| `7aa85df1a51ab1d059d73cf727da91e1` | 2 / 2 | HTTP 200, updated=true, errors=[], RECONCILED |
| `ee9d986b0f28d5d569b8ab4e24843421` | 1 / 1 | HTTP 200, updated=true, errors=[], RECONCILED |
| `f7853b1c3f79e8e524525f848cf0cb1b` | 2 / 2 | HTTP 200, updated=true, errors=[], RECONCILED |
| `84cf0fa821b26381a9a00e058b885233` | 0 / 0 | HTTP 200, updated=true, errors=[], RECONCILED |

The reserved unit belonged to a dedicated zero-price MoySklad test customerorder;
this is not a fabricated Ozon posting or buyer event. The existing calculator
produced 1 from physical 2/reserve 1, then 2 after releasing only that order's
reserve. The source was reduced to zero only after quantity 2 reconciled.
Retained fixtures: enter `fa5196ab-d88d-11f1-0a80-0bf300860f46`, customerorder
`14de5c39-d88e-11f1-0a80-16d60088a0bf` (reserve 0), and loss
`f6073cb6-d892-11f1-0a80-1ff3008d2231`. No payment or shipment was created.

This live run exposed an admission defect: operation
`c8b3838161f66242d5d7d42e35c89e51` exhausted its HTTP timeout while waiting for
the shared target cooldown, before obtaining a write slot. The original UNKNOWN,
null HTTP status and failed single attempt remain historical facts. After stopping
the prior workers, fresh observations and an explicit uncertainty acknowledgment,
audit `6821e489bfc0372069810d2a22ce2aa4` superseded its target fence; it did not
replay or rewrite the original attempt and does not establish external ordering.

The correction keeps a publication queued until the shared database admission
conditions permit a claim. A transport witness preserves positively established
non-dispatch despite net/http timeout wrapping; such attempts become BLOCKED /
PROVIDER_REQUEST_NOT_SENT. Once transport dispatch starts, lost responses remain
UNKNOWN and are never automatically replayed. The transport still owns actual
provider admission; the queue predicate does not reserve a slot. Subsequent live
operations stayed QUEUED during cooldown and reconciled normally. Measured last
write intervals were **120.208632 s and 120.157130 s**.

Three subsequent completed cycles were NO_CHANGE, without publication IDs or
external writes. Reused source batch 61 retained its original timestamps/content
hash while new successful traversal evidence advanced. Final independent reads
after disabling automation: Ozon present/reserved/free **0/0/0** at 21:08:09 UTC;
MoySklad physical/reserve **0/0** at 21:08:14 UTC. Actual network totals:
**Ozon 75 reads + 4 writes; MoySklad 94 reads + 4 writes, all 177 HTTP 200**.
The planned limits were Ozon 200 reads/4 writes and MoySklad 150 reads/8 writes.
These include live normal imports/readbacks, not merely hand-picked diagnostics.

Private journal: `.local/orders-v10/stock-reservation-live-20261104.json`.
Its legacy source intent summary initially said quantity 1 although the approved
phase and serialized request were quantity 2; an append-only correction preserves
both original evidence and the body hash. Two private orchestration assertions
were corrected without an additional provider write. No evidence was overwritten.
Original pretest business settings were restored as audited revision **39 OFF**;
new temporal metadata was not represented as byte-identical restoration. Two APIs
and two workers were restarted from clean `ded6a74` without publication flags.
Schema 60, credentials and key remain intact; final active calls, pending tasks,
dispatches, unresolved target fences and temporary automation grants were zero.

Verification of this correction: make lint/test/build, full Go race and
integration-tag lint passed (lint 0). Three focused real-PostgreSQL/normal-worker
regressions cover pre-network timeout, shared target cooldown and post-dispatch
lost response. The broader stock PostgreSQL race selection completed **68 PASS /
5 SKIP / 0 FAIL** in 86.707 s; skips are four separately opted-in browser tests
and a parent-invoked crash helper. These skips are not counted as passes. Live
active Ozon ship/delivery transitions and unfinished superseded-parent transfer
are not established by this reservation/stock experiment.

The post-correction nine-OS-process browser/race gate also passed in **131.390 s**
(two Playwright journeys passed). It exercised cross-replica sessions/isolation,
shared cooldown, concurrent approvals, independent progress beside UNKNOWN,
kill/restart at claim/dispatch/poll boundaries, stale-worker fencing and credential
rotation/expired grants. Its 1,069 actual loopback HTTP attempts had maximum
in-flight 1; these are deterministic local-provider measurements, not live Ozon
throughput. Current desktop/mobile assembly-reference screenshots were inspected;
unknown quantities remain explicit and the mobile card fits the viewport.

### Positive superseded-parent reserve transfer — 2026-11-05

The earlier rejected superseded-parent candidate was replaced with a hardened,
integrated path (`36cd4bc`, `eea9d32`, `9cfd2ab`, `0d056dd`, `7a39345`). The
[official detail schema](https://docs.ozon.ru/api/seller/zh/swagger.json), reviewed
again on November 5, describes `cancelled_from_split_pending` separately from
ordinary cancellation. The decoder recognizes that exact value without making
ordinary cancellation or generic HTTP 404 sufficient authority to release stock.

The existing immutable split work/preview/receipt, current raw parent status,
complete conserving child observations and native UUID ownership now derive a
transient permission. The durable command stores only the split work identity
and its existing frozen source/native frontier. JSON/literal permissions cannot
be supplied as authority. The normal worker rereads the parent, every child and
the native document; final DB-clock checks compare current child heads and
target-scoped fences under the existing dispatch lock. Each intent can change
only one owned reserve to zero, preserving quantity, price, application and
foreign positions. Children unlock only after actual acknowledgment and a
reconciled zero for every owned parent reserve; repeated cycles are no-ops.

Stock attribution remains separate: a fresh complete native point must match the
released native version and precede a successful durable stock traversal. The
old parent is assessed as explicitly **superseded**, not silently excluded by a
child list. UNKNOWN, changed native facts, missing evidence or a later read failure
still block the affected pool. Foreign same-pool reserves stay in the source
aggregate and are deducted once by the existing calculator. A PostgreSQL/HTTP
test found and fixed missing reserve-only ownership enrichment; the completed
pipeline now settles the parent and both children only after those real reads.
No schema migration, credential replacement or historical outcome rewrite occurs.

Verification: 28 split PostgreSQL race test/subtest events passed in 310.725 s;
the new split-to-native-point-to-stock-traversal case passed in 24.394 s; 39 native
storage events passed in 11.103 s; the existing native collector case passed in
18.216 s. The stock selection passed **68 / skipped 5 / failed 0** in 91.064 s
(the same four opt-in browser tests and parent-invoked crash helper listed above).
The nine-process race/browser gate passed in 130.105 s, with two Playwright
journeys and current desktop/mobile screenshots inspected. Make lint/test/build,
go vet, full race, frontend typecheck/lint/build and 283 frontend tests passed;
normal and integration-tag Go lint report 0. These are deterministic local-provider
integration results, not live Ozon split/shipment claims.

Final guard wiring was rechecked with 9 passing PostgreSQL test/subtest events
in 138.302 s. Four additional regressions ensure a missing/null parent status,
failed native read or UNKNOWN native write cannot disappear behind otherwise
conserving children. Existing unencumbered split coverage remains intact. The
new workflow phases and fixtures are kept bounded without rewriting unrelated
legacy functions.
After those final phase/frontier changes, the new split attribution and existing
native collector PostgreSQL cases passed again together in 45.505 s; full Go
lint/test/build/vet/race and integration-tag lint passed on the final source.

**Remaining scope:** nested split chains and an unreadable/absent old parent do
not acquire permission from this path. A lost acknowledgment is not converted
into a successful HTTP receipt; matching readback alone cannot supply the missing
acknowledged transfer proof. An actual active Ozon replacement-parent transition
has **NOT_OBSERVED_LIVE** coverage until an eligible real posting is available.

A post-deployment **live read-only** gate exercised the clean `7a39345` code on
two API replicas and two workers: **6 Ozon reads + 14 MoySklad reads, all HTTP
200; provider writes 0**. Normal unfulfilled polling was empty; the complementary
creation/detail cycle persisted two historical cancelled postings with explicitly
empty assembly groups. No positive split-retirement status was observed. Two
successful native point attempts reused immutable snapshots/target revisions but
recorded new DB admission and recording times. No complete native account-order
batch, stock publication, order intent or fabricated buyer event was produced.
Private ledger: `.local/orders-v10/superseded-release-read-20261105.json`.
Original disabled order configuration 28 was restored to identical business
contents via audited revision **30 OFF**; stock revision **39 OFF** was unchanged.
Both API replicas passed health/readiness, all four processes used publication
flags OFF, and the original encryption key, credentials and historical outcomes
were preserved. Final active calls, pending tasks and target fences were zero.
