package orderinventory

import (
	"strconv"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
)

func inventoryFixture() Input {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	observed, importID := now.Add(-time.Minute), int64(1)
	pool := domain.StockPool{Tenant: "tenant", MoyskladAccount: "source", WarehouseID: "store", ItemKind: "product", ItemID: "item"}
	baseline := domain.StockSourceEvidence{Tenant: pool.Tenant, Account: pool.MoyskladAccount, WarehouseID: pool.WarehouseID, ItemKind: pool.ItemKind, ItemID: pool.ItemID, ImportID: &importID, ObservationFound: true, Physical: ptr("10"), Reserve: ptr("3"), ObservedAt: &observed, TraversalComplete: true}
	return Input{Pool: pool, Baseline: baseline, BaselineStartedAt: now.Add(-2 * time.Minute), Now: now, MaxAge: 15 * time.Minute, Observations: []Observation{inventoryObservation(now, "posting", "3")}}
}

func inventoryObservation(now time.Time, number, quantity string) Observation {
	scope := orders.Scope{Tenant: "tenant", MoyskladAccount: "source", OzonAccount: "seller"}
	identity := orders.LineIdentity{OfferID: "offer", SKU: "123"}
	posting := orders.Posting{Scope: scope, CommercialOrderID: "commercial", PostingNumber: number, Scheme: orders.Present("FBS"), WarehouseID: orders.Present("warehouse"), Status: orders.Present("awaiting_packaging"), Evidence: orders.EvidenceRevision{Origin: orders.OriginOzon, Revision: "revision", ObservedAt: now.Add(-6 * time.Minute)}, Lines: []orders.PostingLine{{Identity: identity, ProductID: orders.Present("100"), Quantity: orders.Decimal(quantity), CancelledQuantity: orders.Present(orders.Decimal("0"))}}}
	binding := orders.Correlation{Scope: scope, PostingNumber: number, SyncID: "sync-" + number, Marker: "marker-" + number, CustomerOrderID: "native-" + number}
	work := &orders.Work{Scope: scope, State: orders.StateReconciled, Command: orders.Command{Scope: scope, Action: orders.ActionMirrorCreate, PostingNumber: number, ExpectedSourceRevision: posting.Evidence.Revision, Correlation: binding}}
	target := &orders.TargetObservation{Scope: scope, Correlation: binding, Revision: "native-revision", Complete: true, ObservedAt: now.Add(-5 * time.Minute), Fields: orders.CustomerOrderWrite{StoreID: "store", Applicable: orders.Present(true)}, Positions: []orders.TargetPosition{{ID: "position-" + number, MoyskladKind: "product", MoyskladID: "item", Quantity: orders.Decimal(quantity), Reserve: orders.Present(orders.Decimal(quantity))}}}
	position := domain.StockObservedOrderPosition{OrderID: binding.CustomerOrderID, PositionID: target.Positions[0].ID, ItemKind: "product", ItemID: "item", WarehouseID: ptr("store"), Quantity: ptr(quantity), Reserve: ptr(quantity), Shipped: ptr("0"), Applicable: ptr(true), ObservedAt: now.Add(-3 * time.Minute)}
	return Observation{Posting: posting, Lines: []orders.LineIdentity{identity}, Binding: binding, Work: work, Target: target, Positions: []domain.StockObservedOrderPosition{position}, NativeSyncID: ptr(binding.SyncID), NativeStartedAt: now.Add(-4 * time.Minute), NativeFinishedAt: now.Add(-3 * time.Minute)}
}

func ptr[T any](value T) *T { return &value }

func inventoryDecision(input Input) domain.StockDecision {
	pool, observed := input.Pool, *input.Baseline.ObservedAt
	req := domain.StockCalculationRequest{Pool: pool, Target: domain.StockTarget{OzonAccount: "seller", WarehouseID: "warehouse", ProductID: "100"}, Policy: domain.StockCalculationPolicy{Version: 1, Unit: "piece", SafetyStock: "0", MaxObservationAge: input.MaxAge, DecisionLifetime: time.Minute, AcceptOperatorAttested: true}}
	evidence := domain.StockEvidence{Source: input.Baseline, Unreflected: ptr("0"), UnseenBound: ptr("0"), Proofs: make(map[string]domain.StockProof)}
	evidence.ProductMapping = &domain.MappingReference{ID: 1, Version: 1, Tenant: pool.Tenant, MoyskladAccount: pool.MoyskladAccount, OzonAccount: "seller", OzonProductID: "100", OfferID: "offer", ItemKind: pool.ItemKind, ItemID: pool.ItemID, Provenance: "synthetic", ConfirmedAt: observed}
	evidence.WarehouseMapping = &domain.MappingReference{ID: 2, Version: 1, Tenant: pool.Tenant, MoyskladAccount: pool.MoyskladAccount, OzonAccount: "seller", MoyskladWarehouseID: pool.WarehouseID, OzonWarehouseID: "warehouse", Provenance: "synthetic", ConfirmedAt: observed}
	for _, code := range []string{commitment.ProofOwnership, commitment.ProofProductMapping, commitment.ProofWarehouseMapping, commitment.ProofUnit, commitment.ProofBaseline, commitment.ProofSourceOrdering, commitment.ProofUnassigned, commitment.ProofAttribution, commitment.ProofPostingCoverage, commitment.ProofCompeting, commitment.ProofBound} {
		evidence.Proofs[code] = domain.StockProof{State: domain.ProofAttested, EvidenceClass: "OPERATOR_ATTESTED", Reference: "synthetic assumption", AsOf: observed, ValidUntil: input.Now.Add(time.Minute)}
	}
	decision := commitment.Evaluate(req, evidence, input.Now)
	Assess(input).Apply(&decision)
	return decision
}

func TestOrderInventoryBaselineDeductsReserveOnce(t *testing.T) {
	t.Parallel()
	input := inventoryFixture()
	guard, decision := Assess(input), inventoryDecision(input)
	if guard.Blocked || guard.Obligations[0].State != BaselineMatched || decision.Status != domain.StockReady || decision.Quantity == nil || *decision.Quantity != "7" {
		t.Fatalf("matched reserve did not use authoritative calculator once: guard=%+v decision=%+v", guard, decision)
	}
}

func TestOrderInventoryPendingOverridesAttestedZero(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*Input)
	}{
		{"mirror missing reserve", func(i *Input) { i.Observations[0].Target.Positions[0].Reserve = orders.Field[orders.Decimal]{} }},
		{"active explicit zero", func(i *Input) { setReserve(&i.Observations[0], "0") }},
		{"active quantity ten explicit zero", func(i *Input) {
			i.Observations[0] = inventoryObservation(i.Now, "posting", "10")
			setReserve(&i.Observations[0], "0")
		}},
		{"partial reserve", func(i *Input) { setReserve(&i.Observations[0], "2") }},
		{"binding alone", func(i *Input) { i.Observations[0].Work = nil; i.Observations[0].Target = nil }},
		{"unreconciled readback", func(i *Input) { i.Observations[0].Work.State = orders.StateAcknowledged }},
		{"native reserve absent", func(i *Input) { i.Observations[0].Positions[0].Reserve = nil }},
		{"baseline absent", func(i *Input) { i.Baseline.ObservationFound = false }},
		{"native register newer than baseline", func(i *Input) { i.BaselineStartedAt = i.Now.Add(-5 * time.Minute) }},
		{"reserve exceeds baseline", func(i *Input) { i.Baseline.Reserve = ptr("2") }},
		{"tenant conflict", func(i *Input) { i.Observations[0].Posting.Scope.Tenant = "other" }},
		{"account conflict", func(i *Input) { i.Observations[0].Posting.Scope.MoyskladAccount = "other" }},
		{"warehouse unknown", func(i *Input) { i.Observations[0].Positions[0].WarehouseID = nil }},
		{"native origin differs", func(i *Input) { i.Observations[0].NativeSyncID = ptr("foreign-sync") }},
		{"quarantined source", func(i *Input) { i.Observations[0].Quarantined = true }},
		{"bounded observations truncated", func(i *Input) { i.Truncated = true }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := inventoryFixture()
			tc.edit(&input)
			decision := inventoryDecision(input)
			if decision.Status != domain.StockBlocked || decision.Quantity != nil || decision.ExpiresAt != nil || !contains(decision.ReasonCodes, AttributionPending) {
				t.Fatalf("pending attribution bypassed by attested zero: %+v", decision)
			}
		})
	}
}

func setReserve(observation *Observation, value string) {
	observation.Target.Positions[0].Reserve = orders.Present(orders.Decimal(value))
	observation.Positions[0].Reserve = ptr(value)
}

func TestOrderInventoryCancellationPreservesPhysicalAndForeignReserve(t *testing.T) {
	t.Parallel()
	for _, nativeReserve := range []string{"0", "3"} {
		t.Run(nativeReserve, func(t *testing.T) {
			t.Parallel()
			input := inventoryFixture()
			input.Baseline.Reserve = ptr("5")
			input.Observations[0].Posting.Status = orders.Present("cancelled")
			setReserve(&input.Observations[0], nativeReserve)
			decision := inventoryDecision(input)
			if decision.Status != domain.StockReady || decision.Quantity == nil || *decision.Quantity != "5" || *decision.Evidence.Source.Physical != "10" || *decision.Evidence.Source.Reserve != "5" {
				t.Fatalf("cancellation changed physical or foreign reservation: %+v", decision)
			}
		})
	}
}

func TestOrderInventoryNativeShipmentUsesCurrentPhysicalBaseline(t *testing.T) {
	t.Parallel()
	input := inventoryFixture()
	input.Baseline.Physical, input.Baseline.Reserve = ptr("7"), ptr("2")
	input.Observations[0].Posting.Status = orders.Present("delivered")
	setReserve(&input.Observations[0], "0")
	input.Observations[0].Positions[0].Shipped = ptr("3")
	guard, decision := Assess(input), inventoryDecision(input)
	if guard.Blocked || guard.Obligations[0].State != Shipped || decision.Quantity == nil || *decision.Quantity != "5" {
		t.Fatalf("old reservation deducted after native shipment: guard=%+v decision=%+v", guard, decision)
	}
	setReserve(&input.Observations[0], "0")
	input.Observations[0].Positions[0].Shipped = ptr("0")
	if !Assess(input).Blocked {
		t.Fatal("delivered status alone released an obligation")
	}
}

func TestOrderInventorySplitUsesConservedChildrenOnly(t *testing.T) {
	t.Parallel()
	input := inventoryFixture()
	parent := input.Observations[0]
	parent.Posting.ChildPostingNumbers = []string{"child-1", "child-2"}
	parent.Work, parent.Target = nil, nil
	input.Observations = []Observation{parent}
	for index := 1; index <= 2; index++ {
		child := inventoryObservation(input.Now, "child-"+strconv.Itoa(index), strconv.Itoa(index))
		child.Posting.ParentPostingNumber = orders.Present(parent.Posting.PostingNumber)
		input.Observations = append(input.Observations, child)
	}
	guard := Assess(input)
	if guard.Blocked || len(guard.Obligations) != 2 {
		t.Fatalf("parent and children did not use one frontier: %+v", guard)
	}
	input.Observations = input.Observations[:2]
	if !Assess(input).Blocked {
		t.Fatal("partial split silently discarded the parent")
	}
}

func TestOrderInventoryDuplicateNativeOriginIsUnknown(t *testing.T) {
	t.Parallel()
	input := inventoryFixture()
	duplicate := input.Observations[0]
	duplicate.Posting.PostingNumber = "another-posting"
	duplicate.Binding.PostingNumber = duplicate.Posting.PostingNumber
	input.Observations = append(input.Observations, duplicate)
	if !Assess(input).Blocked {
		t.Fatal("one native reservation attributed to multiple postings")
	}
}
