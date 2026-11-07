# ADR 003: Merchant transitions and status ownership

Status: accepted for this implementation; provider live acceptance remains pending.

The merchant operates in MoySklad. Ozon observations, merchant declarations and
external execution results are distinct facts. Mapping two current status strings
cannot establish that a merchant requested an external operation.

STYK retains a native-state baseline only from an actual state-writing mirror
command with a positive provider receipt and reconciled exact readback. An update
may project a new Ozon state only while that baseline still owns the current native
revision. A merchant or foreign metadata edit suspends this ownership as well.
Metadata-only reconciliations never acquire ownership of the observed state.

While the MS-primary lifecycle policy is enabled, an existing customerorder's
state is never included in forward PUTs, even with an unchanged owned baseline.
Only initial creation uses the configured state projection. This avoids claiming
that an unobserved seller edit can be protected by a remote read-before-PUT CAS.
Already journaled merchant intents also fence forward metadata/position commands
until the existing execution fence takes over.

A separate owner-consented `LifecyclePolicy` authorizes exact scheme, state,
predecessor and semantic event mappings. Legacy `AutomationGrant` authority is
unchanged. Saving an expanded or reenabled policy requires independent consent;
the server assigns scope, owner, identity and revisions. Forward projection into a
merchant command state and directed command-state cycles are rejected.

Complete point reads and bounded updated-window polling feed the existing leased
ingestor. The page, checkpoint and immutable merchant transition commit in one
transaction. First observations after a configuration/credential change establish
a baseline. Repeated source revisions and STYK state echoes do not create commands.
The updated list is a hint; the exact scoped order and all positions are reread.
Offset pagination is not a point-in-time snapshot; overlap and periodic exact
keyset reads mitigate, but cannot promise lossless capture of intermediate edits.

Each permitted action has a separate event digest. One rFBS physical handoff may
produce both a carrier command and a native-document intent. Existing per-posting
fences serialize them; the existing preview, approval, outbox, claim token,
single-attempt adapter, UNKNOWN fence and reconciliation remain authoritative.
The local event-to-work binding is a transactional CAS, not exactly-once HTTP.

Before dispatch the worker checks the original native revision/state again,
including after Ozon provider admission. The latter read uses the original bounded
runner context, so it cannot inherit a write guard or another request's dispatch
evidence. MoySklad native writes retain their native revision/position preflight.
Neither provider offers a verified transaction spanning Ozon and MoySklad; a remote
read-to-write race remains and must not be described as atomic provider CAS.

Physical handoff/return means an explicitly configured, newly observed seller
declaration covering the whole posting. Assembly, HTTP 200, `awaiting_deliver`,
`delivered` and carriage approval are not physical evidence. Acceptance-triggered
native automation is rejected until a supported fresh acceptance event is proven.
Unposted parents, unknown linked-demand absence, partial quantities and ambiguous
foreign documents remain reviewed paths. Native gross-consumption, split lineage,
protected events and exact demand/return readback remain mandatory.

Financial realization is a separate authoritative monthly document. No status
event implies payment, commission, report creation or physical return. A separate
owner policy authorizes only the implemented sales-only unposted commission draft
subset, with explicit accountant choices and exact references/totals. Migration 73
uses typed financial work in the existing outbox rather than a fake posting or a
competing queue. Scoped period fencing and immutable source versions prevent
duplicate native reports; uncertain dispatch is read-only, and corrected source
versions produce a reviewable diff rather than a native PUT. Ozon carriage batches
still require their own verified contract/evidence and account/method authority.

Migration 72 is additive and records no authority for old configurations. Its down
migration refuses to discard event or policy history. Roll back runtime behavior by
revoking lifecycle policies and draining/reconciling existing work; use a forward
fix for schema changes once audit exists.
