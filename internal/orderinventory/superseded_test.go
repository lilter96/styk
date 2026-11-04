package orderinventory

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
)

// The PostgreSQL enricher constructs this transient permission through the
// domain's actual split/release/current-child constructor. This fixture tests
// inventory's independent frontier and ordering checks, not that constructor.
func supersededInventoryFixture(t *testing.T, foreign bool) Input {
	t.Helper()
	input := nativePointAttributionFixture()
	o := &input.Observations[0]
	o.Posting.Status = orders.Present(orders.SupersededSplitParentStatus)
	o.Posting.ChildPostingNumbers = []string{"child-a", "child-b"}
	correlation, err := orders.CorrelationFor(o.Posting.Scope, o.Posting.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	correlation.CustomerOrderID = o.Binding.CustomerOrderID
	o.Binding = correlation
	o.NativeSyncID = ptr(correlation.SyncID)
	before := o.Target.Positions[0]
	before.Shipped = orders.Present(orders.Decimal("0"))
	mapping := orders.ProductMappingRef{ID: "mapping", Version: 1, Scope: correlation.Scope, OzonProductID: "100", OfferID: "offer", SKU: "123", MoyskladKind: "product", MoyskladID: "item", Confirmed: true, ConfirmedAt: input.Now.Add(-10 * time.Minute)}
	source := orders.PositionWrite{Identity: o.Lines[0], Mapping: mapping, Quantity: "3", Reserve: orders.Present(orders.Decimal("3"))}
	baseline := orders.PositionBaseline{Scope: correlation.Scope, WorkID: "original-mirror", CommandID: "original-command", Correlation: correlation, ObservedAt: input.Now.Add(-6 * time.Minute), Bindings: []orders.PositionBinding{{Source: source, Native: before}}}
	write := orders.CustomerOrderWrite{OrganizationID: "org", AgentID: "agent", StoreID: input.Pool.WarehouseID, Applicable: orders.Present(true), Positions: []orders.PositionWrite{source}}
	write.Positions[0].Reserve = orders.Present(orders.Decimal("0"))
	command := orders.Command{ID: "release-command", Scope: correlation.Scope, Action: orders.ActionMirrorPositionUpdate, PostingNumber: o.Posting.PostingNumber, CommercialOrderID: o.Posting.CommercialOrderID, Correlation: correlation, ExpectedSourceRevision: o.Posting.Evidence.Revision, ExpectedTargetRevision: "before-release", SupersededSplitWorkID: "actual-split", TargetPositionsComplete: true, TargetPositions: []orders.TargetPosition{before}, CustomerOrder: &write}
	if foreign {
		command.TargetPositions = append(command.TargetPositions, orders.TargetPosition{ID: "foreign-position", MoyskladKind: "product", MoyskladID: "item", Quantity: "7", Reserve: orders.Present(orders.Decimal("2")), Shipped: orders.Present(orders.Decimal("0"))})
	}
	patch, err := orders.PlanPositionPatch(command, baseline)
	if err != nil || patch == nil {
		t.Fatal("fixture release plan", err)
	}
	command.PositionPatch = patch
	write.OwnedFields = patch.Fields()
	o.Work = &orders.Work{ID: "actual-release", Scope: correlation.Scope, State: orders.StateReconciled, DispatchCount: 1, Command: command, Receipt: orders.Receipt{CommandID: command.ID, State: orders.StateAcknowledged, HTTPStatus: 200, ExternalID: correlation.CustomerOrderID}}
	target := *o.Target
	target.Correlation = correlation
	target.Fields = write
	target.Positions = append([]orders.TargetPosition(nil), command.TargetPositions...)
	target.Positions[0].Reserve = orders.Present(orders.Decimal("0"))
	o.Target = &target
	o.Positions[0].Reserve = ptr("0")
	o.Positions[0].Shipped = ptr("0")
	o.Positions[0].Applicable = ptr(true)
	if foreign {
		p := o.Positions[0]
		p.PositionID = "foreign-position"
		p.Quantity = ptr("7")
		p.Reserve = ptr("2")
		o.Positions = append(o.Positions, p)
		input.Baseline.Reserve = ptr("2")
	} else {
		input.Baseline.Reserve = ptr("0")
	}
	o.SplitSuperseded = &orders.SplitReserveTransferPermission{Scope: correlation.Scope, SplitWorkID: "actual-split", ParentWorkID: o.Work.ID, ParentPosting: o.Posting, ParentCorrelation: correlation, ParentTargetRevision: target.Revision, ParentStoreID: target.Fields.StoreID, ParentOrganizationID: target.Fields.OrganizationID, ParentPositions: append([]orders.TargetPosition(nil), target.Positions...), ConfirmedAt: target.ObservedAt}
	return input
}

func TestSupersededInventoryUsesFreshNativeZeroWithoutDoubleSubtracting(t *testing.T) {
	t.Parallel()
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned zero", true: "foreign same pool reserve"}[foreign], func(t *testing.T) {
			t.Parallel()
			input := supersededInventoryFixture(t, foreign)
			guard := Assess(input)
			want := "0"
			quantity := "10"
			if foreign {
				want = "2"
				quantity = "8"
			}
			if guard.Blocked || len(guard.Obligations) != 1 || guard.Obligations[0].State != Superseded || guard.Obligations[0].Reserve != want {
				t.Fatalf("retired parent did not preserve actual reserve: %+v", guard)
			}
			decision := inventoryDecision(input)
			if decision.Status != domain.StockReady || decision.Quantity == nil || *decision.Quantity != quantity {
				t.Fatalf("reserve double-subtracted: %+v", decision)
			}
		})
	}
}

func TestSupersededInventoryMissingOrChangedProofNeverSettles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*Input)
	}{
		{"missing current child proof", func(i *Input) { i.Observations[0].SplitSuperseded = nil }},
		{"latest native failure", func(i *Input) { i.Observations[0].NativeReadCode = "NATIVE_READ_FAILED" }},
		{"own reserve resurged", func(i *Input) { i.Observations[0].Positions[0].Reserve = ptr("1") }},
		{"source missing status", func(i *Input) { i.Observations[0].Posting.Status = orders.Field[string]{} }},
		{"wrong release identity", func(i *Input) { i.Observations[0].SplitSuperseded.ParentWorkID = "other-work" }},
		{"wrong split identity", func(i *Input) { i.Observations[0].SplitSuperseded.SplitWorkID = "other-split" }},
		{"new unresolved work", func(i *Input) { i.Observations[0].Work.State = orders.StateUnknown }},
		{"missing native point", func(i *Input) {
			i.Observations[0].NativePointAdmittedAt = time.Time{}
			i.Observations[0].NativePointRecordedAt = time.Time{}
		}},
		{"old native point", func(i *Input) { i.Observations[0].NativePointAdmittedAt = i.Now.Add(-i.MaxAge) }},
		{"point before release", func(i *Input) { i.Observations[0].NativePointAdmittedAt = i.Observations[0].ReconciledAt }},
		{"source before point", func(i *Input) { i.Baseline.FreshTraversal.StartedAt = i.Observations[0].NativePointRecordedAt }},
		{"missing source baseline", func(i *Input) { i.Baseline.ObservationFound = false }},
		{"foreign same pool reserve changed", func(i *Input) { i.Observations[0].Positions[1].Reserve = ptr("1") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := supersededInventoryFixture(t, true)
			tc.edit(&input)
			if guard := Assess(input); !guard.Blocked {
				t.Fatalf("invalid retirement settled: %+v", guard)
			}
		})
	}
}

func TestSupersededInventoryParentIsNotExcludedBeforeNativeFailure(t *testing.T) {
	t.Parallel()
	input := supersededInventoryFixture(t, false)
	input.Observations[0].NativeReadCode = "NATIVE_ORDER_NOT_FOUND"
	excluded, _ := reservationFrontier(input.Observations)
	if excluded[observationKey(input.Observations[0].Posting)] {
		t.Fatal("provider child names hid parent native failure")
	}
	guard := Assess(input)
	if !guard.Blocked || len(guard.Obligations) != 1 || guard.Obligations[0].State != Unknown {
		t.Fatalf("failed parent dropped from inventory obligations: %+v", guard)
	}
	independent := inventoryFixture()
	if Assess(independent).Blocked {
		t.Fatal("independent known inventory blocked")
	}
}
