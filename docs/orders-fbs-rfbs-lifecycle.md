# FBS/rFBS lifecycle: implementation and acceptance boundaries

## Baseline and gap audit

On 2026-11-06 `git fetch origin --prune` established `origin/master` at
`340c6f884046e8119de6b328019ed4c72b1cb303`. The working tree was initially clean.
The subsequent requested `git pull --ff-only origin master` reported already up
to date. Migration 71 already belongs to product mapping replacement; this change
uses **72–73**, without modifying that migration. Development branch:
`codex/orders-fbs-rfbs-lifecycle`. No push, merge or deployment is authorized here.

Existing production code already contained v4 Ozon list/unfulfilled reads, v3
detail reads, v4 assembly/split, cancellations, country/exemplar requirements,
rFBS capability guards, labels, native customerorder/reserve, reviewed native
demand/return, stock commitment, gross consumption/split lineage, leases/outbox,
single-attempt writes, UNKNOWN reconciliation and the Orders console. They are
reused. The original reverse suggestion was read-only and forward updates could
overwrite a merchant's state. There was no merchant transition journal, independent
lifecycle consent, automatic native physical-event path or realization/native commission importer.

This implementation adds those execution/diagnostic paths and conservative status
ownership, and a separately consented monthly native commission draft pipeline.
Carriage batch operations remain an explicit product gap pending the current
discovery contract and canonical approval evidence. See
[ADR 003](adr/003-merchant-order-intents.md).

## Authority and effect matrix

| Source event | Authority | Action | Native document/effect | Required proof | Durable identity | Expected readback |
| --- | --- | --- | --- | --- | --- | --- |
| New seller-fulfilled posting | Ozon | Existing mirror create | Exact customerorder, configured owned reserve | Fresh source, product/warehouse mappings, references, mirror consent | Existing scoped correlation and work | Exact native order/position readback |
| Native ready transition | Seller in MoySklad | `/v4/posting/fbs/ship` | No demand or stock decrement | Newly observed allowed predecessor/revision, separate lifecycle consent, explicit single-package whole-posting declaration, fresh capability and packing requirements | Merchant digest → one existing outbox work | Expected packing result or verified scheme-specific successor |
| Native physical handoff transition | Seller in MoySklad | Optional native demand | One applied demand linked to exact customerorder | Explicit whole-posting physical declaration, handoff mode, posted parent, native limits, known empty linked demands, owned positions/gross allowance | Protected physical event plus canonical native intent | Exact applied document and parent stock/reserve effect |
| Same handoff for non-integrated rFBS | Seller + verified provider capability | Optional `/v2/fbs/posting/delivering` | No native stock effect from this HTTP operation | Separate mapped action, exact non-integrated delivery profile, predecessor, event reference, current advertised capability | Separate action digest, same source transition; target fence serializes | Verified delivery successor |
| Last-mile/delivered declaration for own rFBS delivery | Seller, only where contract permits | Existing last-mile/delivered adapter | No payment or commission report | Exact profile/action, event-specific mapped transition, predecessor and fresh rereads | Separate merchant action digest | Profile-specific observed outcome |
| Carrier-managed rFBS movement | Carrier/Ozon | Observation only | No seller-driven delivery command | Integrated/aggregator profile | Existing source observation | Carrier's observed state |
| Physical return transition | Seller in MoySklad | Native salesreturn | One applied return linked to exact reconciled demand | Whole-posting return declaration, unique applied owned basis, remaining quantities, protected physical event and readback | Separate canonical return event | Exact demand/return relationship and restored stock |
| Cancellation before movement | Ozon | Existing owned reserve policy | Only owned reserve changes; no return | Exact source and ownership/split guards | Existing projection work | Native owned reserve outcome |
| Cancellation after movement | Ozon | Exception/review | No automatic inverse movement | Physical return evidence required separately | Original movement history retained | No duplicate demand or inferred return |
| Monthly realization becomes available | Ozon finance report + separately consented accountant settings | `/v2/finance/realization` → reviewed or monthly scheduled draft | One **unposted** `commissionreportin`; no stock/payment effect | Closed period, actual Commission/None contract, explicit price/rate/VAT, mapped sales-only rows, exact totals and unsupported components zero | Scoped period head, immutable source versions, sealed plan and one-dispatch shared outbox work | Exact native draft header/positions/references/rate/totals; ACK is insufficient |
| Realization source correction | New authoritative report fingerprint | Versioned row/header diff, manual review | Original report is never silently updated | Retained original/latest source snapshots | Immutable report fingerprints and original work | Changed/added/removed source row numbers; no second POST or PUT |

## What happens after a seller changes a MoySklad state

1. Scheduler works without a browser session. Current source permission and
   credential revisions authorize existing leased collection.
2. The new `moysklad-order-changes` stream requests one bounded page of
   `GET entity/customerorder` with documented `updated>=from;updated<to`,
   `order=updated,asc;id,asc`, `limit` and `offset`. Upper time is fixed before
   traversal, delayed two seconds and aligned to milliseconds. A completed
   checkpoint advances the watermark with five minutes overlap; initial/new
   configuration windows cover fifteen minutes. Outage catchup is bounded to
   31 days per window. Existing periodic exact keyset reads remain enabled.
3. Only exact locally bound IDs are reread, including correlation and complete
   positions. Unknown/deleted/rebound records never create a command. The page,
   source read and checkpoint commit with the detected transitions. Successful
   deduplicated snapshots retain fresh read-attempt clocks and credentials.
4. The observer compares actual before/after state, revision and source update
   time with the configured predecessor and exact scheme. A first read, stale or
   overlapping reads, rotated credentials, configuration changes and STYK's
   own state echoes establish no merchant command.
5. A separate `LifecyclePolicy` must match owner, pair, current configuration,
   action and evidence semantics. Its authority does not come from a mirror
   grant. A physical event can yield separate carrier/native intents.
6. The existing planner checks source freshness, confirmed mappings, native
   lineage and provider prerequisites. Packing uses exactly one package only
   when the owner selected that whole-posting workflow. It never invents country,
   CIS, exemplar, tracking or cancellation parameters. Incomplete cases become
   `BLOCKED` with a local remediation code.
7. Approval binds an event to at most one existing work/outbox row in the same
   transaction. Oldest eligible intents are selected; pending fenced postings
   cannot monopolize the batch. Multiple scheduler processes may review the same
   event; the transactional binding CAS chooses one work. For a combined rFBS
   handoff, the carrier command is admitted before native demand can change the
   customerorder revision. Pending paired commands cannot run in the opposite
   order; each retains its own outcome/fence.
   An observed pending intent also blocks forward metadata/position updates, so
   STYK cannot invalidate that intent's native revision with its own mirror.
8. The worker repeats policy, actor, configuration, credential, mapping and
   exact native revision checks. It rereads the native intent after Ozon admission
   waiting. Native documents retain the provider's complete parent/position
   preflight. No transaction spans HTTP.
9. ACKNOWLEDGED remains an acceptance receipt. A timeout or ambiguous response
   remains fenced and only reads can reconcile it. A revoked policy blocks an
   unsent write; previously dispatched work can still be read and reconciled.

The journal's `QUEUED` means bound to work, not business success. Orders operation
history owns dispatch, UNKNOWN, ACKNOWLEDGED and RECONCILED certainty. Blocked
intents are retained, not silently replayed after a later configuration change.
Transient planning reads remain observed and may be retried within the bounded
intent lifetime; a proved revision mismatch requires intervention.

## FBS/rFBS and native accounting restrictions

- FBS packing does not prove physical handoff. Automatic demand uses only a
  separately mapped `handoff_confirmed` seller declaration.
- With the lifecycle policy enabled, forward updates never write an existing
  native order's state. Initial creation still uses the configured projection;
  Ozon's subsequent observed state stays visible separately. Outside this mode,
  legacy forward state writes require the exact unchanged own-state baseline.
- Integrated/aggregator rFBS does not gain seller delivery authority. Non-integrated
  rFBS still passes existing exact carrier, method, Express/destination and
  advertised-action guards. Unknown/contradictory profiles stay blocked.
- Automatic actions require current advertised capabilities; omitted action
  lists do not grant new automatic authority. Existing reviewed paths keep their
  separately documented contract behavior.
- `ON_OZON_ACCEPTANCE_CONFIRMED` cannot enable automatic demand: this version has
  no verified fresh acceptance event for all supported profiles. It is shown as
  unavailable with the handoff/manual alternative. `not_accepted`, assembly,
  registration, carriage approval and delivered are not acceptance substitutes.
- Automatic native demand requires a posted native parent and proved linked-demand
  absence. A draft/foreign demand cannot be adopted or duplicated. Missing/null
  native link coverage requires review. Reviewed unposted-parent behavior remains
  available under the existing separate policy.
- Partial/split/multiple-package/marked flows retain their existing reviewed
  interfaces and strict lineage/quantity guards. New automation supports explicit
  whole-posting declarations only. No new guessed partial quantity allocation is
  introduced. A return never reopens gross shipment allowance.
- A native state change is a seller declaration under the configured semantics,
  not independent physical telemetry. Configure physical states only for the
  actual warehouse handoff/return confirmation step.

## Provider contract evidence

Checked primary MoySklad sources include the published JSON API 1.2 documentation
and its matching source revision
`fb6d231b292de82379575e3176dd94347e36cf0c`:
[customerorder](https://github.com/moysklad/api-remap-1.2-doc/blob/fb6d231b292de82379575e3176dd94347e36cf0c/md/documents/_customerOrder.md),
[general pagination/sorting](https://github.com/moysklad/api-remap-1.2-doc/blob/fb6d231b292de82379575e3176dd94347e36cf0c/md/_general.md),
[demand](https://dev.moysklad.ru/doc/api/remap/1.2/#/documents/demand),
[salesreturn](https://dev.moysklad.ru/doc/api/remap/1.2/#/documents/salesreturn),
[webhooks](https://dev.moysklad.ru/doc/api/remap/1.2/#/dictionaries/webhook).
The updated-window request/response contract is exercised in
`order_changes_test.go`; fixtures are synthetic, never customer captures.

Webhook delivery is best-effort and tariff/admin restricted; error 403/30006 is
not a source of polling failure. This implementation registers no webhook and
always uses polling. Duplicate/lost webhooks therefore cannot trigger writes.
Polling does not promise every intermediate transition, atomic snapshots,
complete account visibility or exactly-once HTTP. Exact reads detect missing or
deleted known documents; they do not adopt recreated documents by name.

Current Ozon schema verification, accounting field gaps and label/carriage
restrictions are recorded in
[finance contract notes](orders-finance-contract-2026-11-06.md),
[readback/carriage notes](orders-ozon-readback-carriage-2026-11-06.md) and
[delivery capabilities](delivery-capabilities.md). Carriage creation implicitly
includes all ready postings for the method; a per-posting intent cannot safely
authorize that batch. No carriage write is implemented here. No retired finance
transaction endpoint or guessed accounting mapping is introduced.

## Migration and rollback

Apply migrations 72–73 only with both application entry points upgraded and on an
isolated validation database first. It adds scoped immutable transition payloads,
dedupe, one-to-one work binding and audit retention guards. Migration 73 adds
sanitized source versions, expiring financial previews, canonical period fencing,
typed finance work/attempt history and a task type in the **existing** outbox.
Old configurations have neither lifecycle nor finance authority. Startup requires
schema 73 and the scoped ledger constraints; a TCP connection alone is insufficient.

Revoke the lifecycle policy to stop new automatic work. Do not delete UNKNOWN
work or repeat an unconfirmed write. Preserve original receipts and reconcile
exact provider identities. A runtime rollback must understand existing work or
drain it first; use a forward fix after event/policy history exists. Down refuses
to erase event, policy or financial audit. Revoke financial automation separately;
already dispatched UNKNOWN work remains read-only. No migration was applied to shared development or production in
this task.

## Monthly commission workflow

The financial section of Orders settings reads actual native commission contracts
and requires separate owner consent. The owner explicitly chooses price basis,
exchange rate and VAT; no order status grants accounting authority. Manual review
shows exact source/native totals and source revision differences. Optional monthly
scheduling checks the previous closed period from day five every six hours, through
the same sealed preview, approval, shared outbox and one-attempt worker path.

The implemented subset accepts May 2024 onward, at most 1,000 mapped sales lines,
explicit zero returns and zero unsupported components, with exact selected-price,
quantity, signed commission and source-total consistency. Missing/null values,
fractional minor-unit rounding, legacy/nonzero return/loyalty/service components,
VAT-exclusive taxation and uncertain references block before POST. These are
explicit implementation restrictions, not assertions that the provider prohibits
other imports. See the [precise accounting contract](orders-finance-contract-2026-11-06.md).

The native adapter sends `applicable=false`, exact native references and explicit
rewards. It verifies exact marker absence before one POST and rereads the draft's
header, positions and unchanged header afterward. An ACK waits for that readback;
lost ACK remains UNKNOWN and cannot be resent. A changed report stores a new
sanitized source version and requires correction review; the original native
document is never overwritten or reposted. Separate manual documents remain an
external reconciliation risk because neither provider offers cross-system CAS.

## Review inventory

The complete local change inventory is in
[orders-lifecycle-files.txt](orders-lifecycle-files.txt). It includes tests-only
repairs of seven stale Products expectations independently reproduced on clean
`origin/master@340c6f8`; behavioral assertions were preserved. No Product runtime
code was changed for those repairs.

## First three sellers: controlled acceptance

For each seller independently:

1. Read-only preflight: verify tenant/account ownership, API rights, actual
   scheme/carrier/Express/destination, exact SKU/warehouse mappings, native state
   IDs, posted-parent behavior and linked-document visibility. Check a closed
   finance period separately. Do not infer capabilities from warehouse names.
2. Record explicit pair-scoped consent for each enabled action and physical
   declaration, single-package composition and native quantity limits. Begin with
   one unmarked whole posting. Keep other optional automations off.
3. Establish the initial observation baseline. Move the order in MoySklad and
   watch one intent/work, one provider attempt, ACK and actual readback. Independently
   read native stock/reserve before and after. Do not count HTTP 200 as completion.
4. Only after controlled success, enable native handoff for the seller's actual
   workflow and verify `10 physical / 1 reserve / 9 available` becomes
   `9 / 0 / 9` exactly once. Test a genuine linked return separately.
5. On UNKNOWN, unexpected native links or stale identity, revoke automation,
   retain the fence and resolve through exact reads/manual review. Do not reverse
   an accounting movement solely because Ozon cancelled or delivery failed.
6. Review the actual closed-month source with the accountant. Independently opt
   into finance, select the real contract/currency/rate/price/VAT and first approve
   one **unposted** draft manually. Check its actual positions, signed commission
   and totals in MoySklad before enabling monthly scheduling. Corrected reports,
   returns or unsupported service components require manual accounting review.
7. Monitor scoped failed reads, freshness, intent blockers, permission/rate-limit
   cooldowns and operation certainty. Do not expose customer payloads in logs.

Live active Ozon assembly/delivery, real per-seller physical handoff and production
monthly accounting remain **NOT_OBSERVED_LIVE**. Contract fixtures establish
deterministic behavior, not unrestricted production readiness.


## Authorized LIVE follow-up, 2026-11-07

The subsequent [production-account acceptance](live-orders-acceptance-2026-11-07/executive-summary.md)
used four actual provider accounts, inspected 380 postings and seven monthly
realization sources, and performed five independently read-back-controlled native
writes on one empty, unposted neutral test asset. Shared development was backed
up and upgraded to schema 73; no production deployment, push or merge occurred.
Active packing, physical handoff/return and an eligible real commission draft
remain unobserved. Source parsing and account metadata evidence must not be
mistaken for completion of those actions. See the exact scenario matrix and
remaining seller gates in the linked acceptance report.
