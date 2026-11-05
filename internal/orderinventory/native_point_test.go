package orderinventory

import (
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
)

func ownedSamePoolPointFixture(t *testing.T) Input {
	t.Helper()
	input := nativePointAttributionFixture()
	o := &input.Observations[0]
	binding, err := orders.CorrelationFor(o.Posting.Scope, o.Posting.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	binding.CustomerOrderID = o.Binding.CustomerOrderID
	o.Binding, o.Work.Command.Correlation, o.Target.Correlation = binding, binding, binding
	o.NativeSyncID = ptr(binding.SyncID)
	o.Work.ID, o.Work.Command.ID = "native-create", "native-create-command"
	source := ownedSamePoolSource(input, *o)
	o.Work.Command.CustomerOrder = &orders.CustomerOrderWrite{StoreID: input.Pool.WarehouseID, Positions: []orders.PositionWrite{source}}
	baseline, err := orders.ProjectionPositionBaseline(*o.Work, *o.Target)
	if err != nil {
		t.Fatal(err)
	}
	appendSamePoolForeignPosition(o)
	o.Work.Command.TargetPositions, o.Work.Command.TargetPositionsComplete = slices.Clone(o.Target.Positions), true
	o.Work.ID, o.Work.Command.ID, o.Work.Command.Action = "native-reduction", "native-reduction-command", orders.ActionMirrorPositionUpdate
	o.Work.Command.PositionPatch = &orders.PositionPatch{Baseline: baseline, PositionID: o.Target.Positions[0].ID, Identity: source.Identity, Quantity: orders.Present(orders.Decimal("1")), Reserve: orders.Present(orders.Decimal("1"))}
	o.Work.Command.CustomerOrder.Positions[0].Quantity, o.Work.Command.CustomerOrder.Positions[0].Reserve = "1", orders.Present(orders.Decimal("1"))
	o.Target.Positions[0].Quantity, o.Target.Positions[0].Reserve = "1", orders.Present(orders.Decimal("1"))
	o.Posting.Lines[0].Quantity, o.Positions[0].Quantity, o.Positions[0].Reserve = "1", ptr("1"), ptr("1")
	return input
}

func ownedSamePoolSource(input Input, o Observation) orders.PositionWrite {
	identity := o.Posting.Lines[0].Identity
	mapping := orders.ProductMappingRef{ID: "mapping", Version: 1, Scope: o.Posting.Scope, OzonProductID: "100", SKU: identity.SKU, OfferID: identity.OfferID, MoyskladKind: input.Pool.ItemKind, MoyskladID: input.Pool.ItemID, Confirmed: true, ConfirmedAt: input.Now.Add(-time.Hour)}
	return orders.PositionWrite{Identity: identity, Mapping: mapping, Quantity: "3", Reserve: orders.Present(orders.Decimal("3"))}
}

func appendSamePoolForeignPosition(o *Observation) {
	foreign := o.Target.Positions[0]
	foreign.ID, foreign.Quantity, foreign.Reserve = "foreign-same-product", "7", orders.Present(orders.Decimal("2"))
	o.Target.Positions = append(o.Target.Positions, foreign)
	observed := o.Positions[0]
	observed.PositionID, observed.Quantity, observed.Reserve = foreign.ID, ptr("7"), ptr("2")
	o.Positions = append(o.Positions, observed)
}

func TestNativePointOwnedSamePoolForeignReserveUsesBaselineOnce(t *testing.T) {
	t.Parallel()
	input := ownedSamePoolPointFixture(t)
	guard, decision := Assess(input), inventoryDecision(input)
	if guard.Blocked || len(guard.Obligations) != 1 || guard.Obligations[0].Reserve != "1" || decision.Status != domain.StockReady || decision.Quantity == nil || *decision.Quantity != "7" {
		t.Fatalf("owned reserve was confused with foreign same-product reserve: guard=%+v decision=%+v", guard, decision)
	}
	input.Observations[0].Positions[1].Reserve = nil
	if !Assess(input).Blocked {
		t.Fatal("UUID ownership hid an unknown foreign reserve")
	}
	input = ownedSamePoolPointFixture(t)
	input.Observations[0].Work.Command.PositionPatch = nil
	if !Assess(input).Blocked {
		t.Fatal("ambiguous same-product rows were selected without UUID proof")
	}
}

func TestNativePointForeignCoverageStillConstrainsTheWholePool(t *testing.T) {
	t.Parallel()
	input := ownedSamePoolPointFixture(t)
	other := inventoryObservation(input.Now, "another-order", "4")
	input.Observations = append(input.Observations, other)
	input.Baseline.Reserve = ptr("5")
	guard, decision := Assess(input), inventoryDecision(input)
	// Owned obligations total five, but actual known native reserves total seven.
	// The foreign two cannot be discarded merely because they are not ours.
	if !guard.Blocked || decision.Quantity != nil || decision.Status != domain.StockBlocked {
		t.Fatalf("foreign reserve disappeared from baseline coverage: guard=%+v decision=%+v", guard, decision)
	}
}

func nativePointAttributionFixture() Input {
	input := inventoryFixture()
	original := input.Now.Add(-6 * time.Minute)
	input.Baseline.ObservedAt = &original
	input.BaselineStartedAt = input.Now.Add(-7 * time.Minute)
	observation := &input.Observations[0]
	observation.ReconciledAt = input.Now.Add(-5 * time.Minute)
	observation.NativePointAdmittedAt = input.Now.Add(-4 * time.Minute)
	observation.NativePointRecordedAt = input.Now.Add(-3 * time.Minute)
	input.Baseline.FreshTraversal = &domain.StockTraversalEvidence{OperationID: "db-admitted-stock-read", ImportID: *input.Baseline.ImportID, StartedAt: input.Now.Add(-2 * time.Minute), FinishedAt: input.Now.Add(-time.Minute)}
	return input
}

func TestNativePointAttributionRequiresDBAdmissionAndFreshStockTraversal(t *testing.T) {
	t.Parallel()
	input := nativePointAttributionFixture()
	guard := Assess(input)
	if guard.Blocked || len(guard.Obligations) != 1 || guard.Obligations[0].State != BaselineMatched {
		t.Fatalf("ordered point proof did not settle: %+v", guard)
	}
	decision := inventoryDecision(input)
	if decision.Quantity == nil || *decision.Quantity != "7" {
		t.Fatalf("reserve was not deducted exactly once: %+v", decision)
	}
}

func TestNativePointAttributionRejectsIncompleteDBOrder(t *testing.T) {
	t.Parallel()
	for _, tc := range nativePointOrderingCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := nativePointAttributionFixture()
			tc.alter(&input)
			if !Assess(input).Blocked {
				t.Fatal("incomplete point ordering admitted a stock candidate")
			}
		})
	}
}

type nativePointOrderMutation struct {
	name  string
	alter func(*Input)
}

func nativePointOrderingCases() []nativePointOrderMutation {
	return []nativePointOrderMutation{
		{"missing reconciliation clock", func(i *Input) { i.Observations[0].ReconciledAt = time.Time{} }},
		{"missing DB admission", func(i *Input) { i.Observations[0].NativePointAdmittedAt = time.Time{} }},
		{"missing DB recording", func(i *Input) { i.Observations[0].NativePointRecordedAt = time.Time{} }},
		{"admission before reconciliation", func(i *Input) {
			i.Observations[0].NativePointAdmittedAt = i.Observations[0].ReconciledAt.Add(-time.Second)
		}},
		{"admission equals reconciliation", func(i *Input) { i.Observations[0].NativePointAdmittedAt = i.Observations[0].ReconciledAt }},
		{"recording before admission", func(i *Input) {
			i.Observations[0].NativePointRecordedAt = i.Observations[0].NativePointAdmittedAt.Add(-time.Second)
		}},
		{"recording equals admission", func(i *Input) { i.Observations[0].NativePointRecordedAt = i.Observations[0].NativePointAdmittedAt }},
		{"stock began before point recorded", func(i *Input) {
			i.Baseline.FreshTraversal.StartedAt = i.Observations[0].NativePointRecordedAt.Add(-time.Second)
		}},
		{"stock began exactly when recorded", func(i *Input) { i.Baseline.FreshTraversal.StartedAt = i.Observations[0].NativePointRecordedAt }},
		{"no durable stock traversal", func(i *Input) { i.Baseline.FreshTraversal = nil; i.BaselineStartedAt = i.Now.Add(-time.Second) }},
		{"different stock content identity", func(i *Input) { i.Baseline.FreshTraversal.ImportID++ }},
		{"future traversal", func(i *Input) { i.Baseline.FreshTraversal.FinishedAt = i.Now.Add(time.Second) }},
		{"physical quantity not whole", func(i *Input) { i.Baseline.Physical = ptr("1.25") }},
	}
}

func TestNativePointAttributionUsesDBOrderingWithoutRewritingClientClocks(t *testing.T) {
	t.Parallel()
	for _, offset := range []time.Duration{-time.Second, time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			t.Parallel()
			input := nativePointAttributionFixture()
			observation := &input.Observations[0]
			pivot := observation.ReconciledAt
			if offset > 0 {
				pivot = observation.NativePointRecordedAt
			}
			observation.NativeStartedAt = pivot.Add(offset)
			observation.NativeFinishedAt = observation.NativeStartedAt.Add(100 * time.Millisecond)
			for p := range observation.Positions {
				observation.Positions[p].ObservedAt = observation.NativeFinishedAt
			}
			started, finished := observation.NativeStartedAt, observation.NativeFinishedAt
			if guard := Assess(input); guard.Blocked {
				t.Fatalf("client clock comparison substituted for DB proof: %+v", guard)
			}
			if !started.Equal(observation.NativeStartedAt) || !finished.Equal(observation.NativeFinishedAt) {
				t.Fatal("read clocks rewritten")
			}
		})
	}
}

func TestNativePointAttributionFailedReadAndChangedFactsStayUnknown(t *testing.T) {
	t.Parallel()
	for _, tc := range []nativePointOrderMutation{
		{"failed latest native read", func(i *Input) { i.Observations[0].NativeReadCode = "NATIVE_READ_FAILED" }},
		{"credential denied", func(i *Input) { i.Observations[0].NativeReadCode = "NATIVE_AUTHORIZATION_DENIED" }},
		{"different native counter", func(i *Input) { i.Observations[0].Positions[0].Reserve = ptr("0") }},
		{"native deleted", func(i *Input) { i.Observations[0].Positions[0].SourceDeleted = ptr("actual-deletion-version") }},
		{"missing native reserve", func(i *Input) { i.Observations[0].Positions[0].Reserve = nil }},
		{"wrong scope", func(i *Input) { i.Observations[0].Binding.Scope.Tenant = "different" }},
		{"source changed after review", func(i *Input) { i.Observations[0].Posting.Evidence.Revision = "newer-source" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := nativePointAttributionFixture()
			tc.alter(&input)
			if !Assess(input).Blocked {
				t.Fatal("failed or changed native point falsely settled")
			}
		})
	}
}

func TestNativePointCancellationCannotHideChangedNativeReserve(t *testing.T) {
	t.Parallel()
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "reserve_reappeared", true: "native_deleted"}[deleted], func(t *testing.T) {
			t.Parallel()
			input := nativePointAttributionFixture()
			observation := &input.Observations[0]
			observation.Posting.Status = orders.Present("cancelled")
			observation.Target.Positions[0].Reserve = orders.Present(orders.Decimal("0"))
			observation.Positions[0].Reserve = ptr("1")
			if deleted {
				observation.Positions[0].Reserve = ptr("0")
				observation.Positions[0].SourceDeleted = ptr("actual-native-deletion")
			}
			if guard := Assess(input); !guard.Blocked {
				t.Fatalf("cached cancelled zero hid changed latest point: %+v", guard)
			}
		})
	}
}

func TestNativePointDemandAdmissionFollowsBothDBConfirmations(t *testing.T) {
	t.Parallel()
	input := nativeDemandFixture(t, 3)
	observation := &input.Observations[0]
	base := observation.NativeConfirmedAt
	observation.ReconciledAt = base.Add(time.Millisecond)
	observation.NativePointAdmittedAt = base.Add(time.Second)
	observation.NativePointRecordedAt = base.Add(2 * time.Second)
	if guard := Assess(input); guard.Blocked || guard.Obligations[0].State != Shipped {
		t.Fatalf("complete shipped proof rejected: %+v", guard)
	}
	observation.NativePointAdmittedAt = base.Add(-time.Millisecond)
	if !Assess(input).Blocked {
		t.Fatal("native point read before movement DB confirmation settled")
	}
}
