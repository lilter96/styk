# Stock commitment engine: proposed test matrix

This is a plan, **not a claim that the new engine or these tests already exist**. All fixtures must be synthetic; unit tests use a fixed clock and pure inputs, HTTP tests use `httptest`, and database tests use a migrated dedicated PostgreSQL test database. No live credentials or external writes. See the [architecture](stock-commitment-architecture.md) and [invariants](stock-commitment-invariants.md).

## Existing evidence and gaps

| Existing behavior | Current tests to inspect | What remains unproved |
| --- | --- | --- |
| MoySklad observations and pagination | `internal/infrastructure/moysklad/client_test.go`: `TestReadPaginationAndValues`, `TestIncompleteAndDuplicates`; `tests/integration/moysklad_test.go`: `TestMoyskladIngestion` | Real nonempty stock contract, source consistency, unassigned reserves, attribution and safe capacity. |
| Ozon catalog/stock observation | `internal/infrastructure/ozon/client_test.go`: `TestRead`, `TestIncompleteStockAndCursor`; `tests/integration/ozon_test.go`: `TestOzonImport` | Current official field and write semantics, hidden reservations, latest state and actual exposure. |
| Posting history and scoped correlation | `tests/integration/postings_history_test.go`: `TestPostingHistoryCancellationCompositionAndOrdering`, `TestPostingCorrelationScopeAndCurrentMappings`; `tests/integration/postings_atomicity_test.go`: `TestPostingImportAtomicityAndRetry`, `TestPostingDatabaseRollbackAndCancellation` | A posting-to-reserve crosswalk, authoritative source ordering, historical mapping, tenant authorization and any commitment calculation. |
| Local mock leases/fencing | `tests/integration/store_test.go`: `TestStore`; `internal/application/worker_test.go`: `TestMockIdempotent` | Fencing an external request, unknown outcomes, exactly-once external effects or safe recovery for real writes. |

These names identify existing repository tests; the scenarios below are proposed acceptance tests and have not been run.

## Read-only V1: deterministic unit cases

Each test should assert the exact result state, NULL versus zero, reasons, original IDs, input evidence references and unchanged previous evidence, not just a successful return. Use table-driven cases where the expectation is independently specified.

| Proposed test | Fixture and expected result | Invariants |
| --- | --- | --- |
| `TestPoolIdentity` | Same textual ID across product/variant, warehouses, source accounts or tenants yields different pools; Ozon product ID, offer ID and SKU remain distinct. | I01–I03 |
| `TestSinglePoolPolicy` | One confirmed product plus one confirmed warehouse mapping in an exclusive owned pool with verified one-unit composition is eligible for further checks; suggestion, changed/foreign mapping, shared target, second offer, second account or marketplace, unverified composition, bundle, kit and multipack are blocked. | I01–I03, I11 |
| `TestFieldMeaningAndMissing` | Preserve physical/reserve/incoming/Ozon fields separately; aggregate is not warehouse, NULL/missing is unknown, a witnessed zero stays zero, incoming never increases V1 capacity. Unsupported negative/fractional/precision inputs are unknown or blocked under the selected policy. | I06–I07, I10 |
| `TestUnassignedReserve` | Warehouse `P=10,R=1` but unassigned reserve is missing: unknown. With proven total and a justified allocation of 2 to this pool, `R=3` for the conditional model. No inferred distribution. | I06–I08 |
| `TestNormalAndUnreflectedCommitment` | With fully synthetic valid gates, before an order `10−1−0−1=8`; a proven unreflected 2-unit order yields 6, not 8. | I07–I10 |
| `TestReflectedCommitmentNoDoubleCount` | Same 2 units change from proven `U=2,R=1` to proven `U=0,R=3`; result remains 6. Equal totals or timestamps without a crosswalk return unknown. Also cover partial reflection and source physical reduction. | I07–I10 |
| `TestOtherChannelAndPrePostingReserve` | Other-channel source reserve stays in `R`; a known unreflected other-channel obligation is in `U` once. An Ozon pre-posting aggregate reserve with unproven overlap with postings or source reserve is unknown, never summed blindly. | I08, I11 |
| `TestCancellationAndDelayedSource` | Active → cancellation, source still reserved: no increase. Late or partial cancellation with no trusted ordering/attribution is unknown; only a proven compatible release permits an increase. Preserve unknown reason. | I05, I08–I10 |
| `TestSplitAndComposition` | Two postings for one order are separate; changed quantity with unchanged status and removed/added products persist as separate evidence. Parent/child overlap without authoritative split relation is unknown; a verified split counts units once. | I04–I05, I08–I09 |
| `TestDuplicateAndOutOfOrder` | Same source event repeated changes projection once; changed payload for same event key conflicts. Identical observations from different batches remain distinct; incomparable active/cancelled states are unknown regardless of arrival order. `A→B→A` is not globally deduplicated. | I04–I05, I12 |
| `TestBaselineStalenessAndCoverage` | Distinguish failed pages, count drift, restricted visibility, recalculation, missing window, clock skew, TTL expiry and unsupported no-watermark coverage. Fresh local finish time alone does not make a baseline safe; fingerprint dedup retains old time. | I06–I07, I13, I17 |
| `TestMappingVersionAndPolicyRevision` | Freeze mappings at interpretation; changing either version/policy invalidates current decision without altering historical evidence. If the affected pool cannot be established, both plausible pools are held. | I03–I04, I13 |
| `TestDecimalConversionAndBounds` | Exact decimal values, zero, large values and conversion boundary; never round upward. Missing conversion, unit mismatch or unsubstantiated `D` blocks. Distinguish a valid final clamp to zero from an invalid negative source value. | I07, I10 |
| `TestAllReasonsAndStableExplanation` | Multiple simultaneous gate failures appear in deterministic order with scoped evidence IDs; result is unknown/blocked with NULL. A ready result includes baseline, policy, mapping versions, expiry and all components. | I10, I12–I13 |
| `TestExposureOnHold` | A read-only hold while a previously observed target is sellable raises an exposure warning; it never claims to have stopped sales or emits an external command. | I11, I17 |

## Property and model-based tests

Use reproducible seeds and print the failing seed plus minimal synthetic event sequence. Generation must use a simple independent oracle, not duplicate the implementation.

| Proposed property | Assertion | Invariants |
| --- | --- | --- |
| `FuzzEvidencePermutation` | Permuting equivalent unordered, non-conflicting evidence leaves the explanation/decision unchanged. When source order is absent and states conflict, every permutation returns unknown rather than choosing the last arrival. | I05, I12 |
| `FuzzRedeliveryAndReversion` | Re-delivering an identified event does not increase its projected obligation; conflicting payload is detected. A genuine later version or `A→B→A` sequence is not discarded. | I04–I05, I08, I12 |
| `FuzzReflectionConservation` | For a proven transition from unreflected to reflected quantity `x`, decreasing `U` and increasing attributable `R` by `x` leaves conditional capacity unchanged. No portion can be attributed twice or beyond its source/commitment amount. | I08, I10 |
| `FuzzMonotonicBounds` | With a fixed valid baseline and all else equal, increasing proven disjoint `R`, `U`, `S`, or `D` never increases conditional `C`; reducing proven `P` never increases it. Unknown inputs never become numeric. | I07–I10 |
| `FuzzScopeAndIdentity` | Arbitrary overlapping identifiers in different tenant/account/item-kind namespaces never correlate without an explicitly scoped relation. | I01–I03 |
| `FuzzAllocationConservation` | Future allocator only: each accepted allocation respects `Σ exposure ≤ C`; moving a proven obligation between representations does not manufacture capacity. Unknown write exposure is held at its safe upper bound. | I08, I11, I15 |
| `TestModelLifecycleSequences` | Generate bounded corrections, splits, partial releases, conflicting timestamps and mapping changes. Replay into a reference state machine; no unsupported transition becomes ready or releases capacity. | I03–I05, I08–I13 |

## PostgreSQL integration and concurrency

Run migrations on a disposable database. Test application roles as well as direct inserts to prove constraints; privileged test setup must not be mistaken for tenant authorization. Inject failures deterministically, then verify both data and projections after a new connection/restart.

| Proposed test | Required assertion | Invariants |
| --- | --- | --- |
| `TestOwnershipAndScopedForeignKeys` | Duplicate physical external account cannot be independently assigned to tenants; cross-tenant/account pool, posting, evidence, mapping-history, decision and attempt references are rejected. Authorized queries cannot read another tenant; direct admin access is explicitly outside tenant guarantees. | I01–I03 |
| `TestImmutableEvidencePermissions` | Application role cannot UPDATE/DELETE evidence or cascade-delete its parent; an append correction succeeds. Previous raw IDs, amounts, observations and mapping references survive. | I04 |
| `TestAtomicEvidenceAndProjection` | Inject failure after evidence insert but before projection/decision insert and vice versa; transaction leaves no partial change. A subsequent retry succeeds exactly once at the projection level and older completed records remain. | I04, I12, I17 |
| `TestSourceEventConflict` | Concurrent identical event insertion produces one event; same scoped key with different payload is rejected and flagged. Repeated non-event observation batches remain represented. | I05, I12 |
| `TestReflectionAllocationRace` | Two transactions each attempt to attribute the same 2 reserved units to different commitments; lock/constraint allows at most the proven quantity, no write skew, and the loser returns conflict or unknown. Repeat with partial portions. | I08 |
| `TestPoolDecisionRevisionRace` | A source/mapping/policy revision changes while a calculation is being finalized; compare-and-set/lock rejects or retries stale decision. No network call under DB transaction. | I03, I13, I16 |
| `TestSharedPoolUniquenessRace` | Concurrent activation for two offers/accounts/targets backed by the same physical pool: at most one V1 active binding; the other is blocked. Ensure scope keys cannot make the same resource appear twice. | I01, I11 |
| `TestWarehouseBindingAcrossAccounts` | Two account pairs try to activate the same physical source warehouse or the same Ozon warehouse; the global one-to-one V1 binding accepts at most one and preserves historical versions. | I01–I03 |
| `TestImportFailureRetainsPrior` | Incomplete page, cancelled import and DB insertion failure produce no completed replacement; earlier observation remains available but may be stale and cannot automatically justify readiness. | I06–I07, I17 |
| `TestProjectionRebuild` | Delete/rebuild projection in an allowed test transaction from immutable evidence; it yields the same state/reasons and preserves historical decisions. Mapping changes do not reinterpret old decisions as historical mappings. | I03–I05, I12–I13 |
| `TestRepeatFingerprintIsNotFreshness` | Two equal import traversals deduplicate in the existing implementation; calculation uses retained old local time and remains stale, unless separately evidenced fresh traversal metadata is later implemented. | I06–I07, I13 |

## Future publication and reconciliation: no V1 writes

These tests are **prerequisites for a later writer**, not authorization to create one. Simulate an external server only; never contact Ozon or use customer data.

| Proposed test | Failure injected and expected result | Invariants |
| --- | --- | --- |
| `TestAcceptedResponseLost` | Mock accepts a stock request then drops response; record attempt `unknown`, preserve exposure and freeze target. Do not automatically retry. | I14–I15, I17 |
| `TestCrashAtEachBoundary` | Crash before durable intent, after commit before send, after possible send, after response before recording, and after recording. Recover by durable facts; only provably unsent work is eligible for resend. | I14–I16 |
| `TestLeaseExpiryExternalOverlap` | Pause worker A after send; expire lease; start B. A can still complete; database fence cannot prevent that. B must not send a conflicting update until safe resolution. | I15–I16 |
| `TestReconcileLatePriorWrite` | Read back expected quantity, then simulate earlier in-flight request applying late. Readback alone must not close unknown without proven quiescence/ordering. | I15, I18 |
| `TestDesiredAttemptedAcknowledgedVerified` | An accepted response and delayed/stale/mismatched readback leave distinct facts; successful response is not a verified current state. | I14, I18 |
| `TestPartialBatchAndDuplicateAttempt` | One target accepted, one rejected, one unknown; no blanket success/failure. A repeated local operation key with same payload is idempotent; different payload conflicts. | I14–I16, I18 |
| `TestStaleDecisionAtSend` | Mapping, pool revision, status, observed source, timeout or uncertainty bound changes after intent creation. The sender refuses stale intent and reconciles already exposed stock. | I03, I13, I16–I17 |
| `TestCompetingExposures` | Two workers/channels try to advertise the same 15-unit pool; total proven upper-bound exposures never exceeds 15 under allocator locks. Unknown outcome retains its maximum. Uncontrolled outside exposure blocks. | I08, I11, I15–I16 |

## Acceptance and execution

Read-only implementation must pass formatting, vet, unit and race tests, build, migrations, PostgreSQL integration and race-enabled integration in isolated infrastructure. Tests for I01–I13 and I17 must assert behavior with deterministic fixtures, including the unknown path when real imported evidence is insufficient. Review composite keys, transaction isolation, lock ordering and privileges. Record actual commands/results in the implementing change; do not present this plan as executed coverage. External write tests are a separate later milestone gated by the [architecture's no-go criteria](stock-commitment-architecture.md#scope-phases-and-go-no-go).
