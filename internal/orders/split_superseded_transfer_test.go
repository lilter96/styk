package orders

import (
	"slices"
	"testing"
)

func supersededTransferFixture(t *testing.T) (SplitReserveTransferFacts, SplitSupersededReserveFacts) {
	t.Helper()
	retirement, command := supersededSplitFixture(t)
	command.Warehouse = retirement.Split.Command.Warehouse
	command.CustomerOrder.StateID, command.CustomerOrder.SalesChannelID = "state", "channel"
	retirement.Target.Fields = *command.CustomerOrder
	proof, err := NewSupersededSplitReserve(retirement)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanSupersededSplitReserve(command, proof)
	if err != nil || plan.PositionPatch == nil {
		t.Fatal("release plan", err)
	}
	command.Action, command.CustomerOrder, command.PositionPatch = ActionMirrorPositionUpdate, &plan.CustomerOrder, plan.PositionPatch
	command.SupersededSplitWorkID = retirement.Split.ID
	target := retirement.Target
	target.Positions = slices.Clone(target.Positions)
	target.Positions[0].Reserve = Present(Decimal("0"))
	target.Revision = "actual-release-frontier"
	release := Work{ID: "actual-parent-release", Scope: command.Scope, Command: command, State: StateReconciled, DispatchCount: 1, Receipt: Receipt{CommandID: command.ID, State: StateAcknowledged, HTTPStatus: 200, ExternalID: command.Correlation.CustomerOrderID}}
	baseline, err := ProjectionPositionBaseline(release, target)
	if err != nil {
		t.Fatal(err)
	}
	retirement.Baseline, retirement.Target = baseline, target
	facts := SplitReserveTransferFacts{Split: retirement.Split, Before: retirement.Before, OriginalCorrelation: retirement.OriginalCorrelation, ParentRelease: release, Parent: retirement.Parent, ParentTarget: target, Child: retirement.Children[1]}
	return facts, retirement
}

func TestSupersededTransferUnlocksOnlyAfterActualEntireOwnedRelease(t *testing.T) {
	t.Parallel()
	facts, retirement := supersededTransferFixture(t)
	proof, err := NewSupersededSplitTransfer(facts, retirement)
	if err != nil || !proof.Allows(facts.Child) {
		t.Fatal("complete positive release blocked", err)
	}
	if proof.Allows(facts.Parent) || *proof.ParentPositions[1].Reserve.Value != "2" || proof.ConfirmedAt != facts.ParentTarget.ObservedAt {
		t.Fatal("parent/foreign frontier changed")
	}
	facts.ParentTarget.Positions[0].Reserve.Value = new(Decimal)
	if !decimalZero(*proof.ParentPositions[0].Reserve.Value) {
		t.Fatal("proof aliases caller")
	}
}

type supersededTransferCase struct {
	name  string
	alter func(*SplitReserveTransferFacts, *SplitSupersededReserveFacts)
}

func TestSupersededTransferRejectsIncompleteOrSubstitutedProof(t *testing.T) {
	t.Parallel()
	cases := append(supersededReleaseCases(), supersededFrontierCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, r := supersededTransferFixture(t)
			tc.alter(&f, &r)
			if _, err := NewSupersededSplitTransfer(f, r); err == nil {
				t.Fatal("invalid release unlocked child")
			}
		})
	}
}

func supersededReleaseCases() []supersededTransferCase {
	return []supersededTransferCase{
		{"unknown release", func(f *SplitReserveTransferFacts, _ *SplitSupersededReserveFacts) {
			f.ParentRelease.State = StateUnknown
		}},
		{"missing acknowledgment", func(f *SplitReserveTransferFacts, _ *SplitSupersededReserveFacts) {
			f.ParentRelease.Receipt.State = StateUnknown
		}},
		{"wrong account", func(f *SplitReserveTransferFacts, _ *SplitSupersededReserveFacts) {
			f.ParentRelease.Scope.OzonAccount = "foreign"
		}},
		{"wrong split marker", func(f *SplitReserveTransferFacts, _ *SplitSupersededReserveFacts) {
			f.ParentRelease.Command.SupersededSplitWorkID = "other"
		}},
		{"two markers", func(f *SplitReserveTransferFacts, _ *SplitSupersededReserveFacts) {
			f.ParentRelease.Command.RetainedSplitWorkID = f.Split.ID
		}},
		{"generic cancel", func(f *SplitReserveTransferFacts, r *SplitSupersededReserveFacts) {
			f.Parent.Status = Present("cancelled")
			r.Parent = f.Parent
		}},
		{"missing parent", func(f *SplitReserveTransferFacts, r *SplitSupersededReserveFacts) {
			f.Parent = Posting{}
			r.Parent = f.Parent
		}},
	}
}

func supersededFrontierCases() []supersededTransferCase {
	return []supersededTransferCase{
		{"incomplete children", func(_ *SplitReserveTransferFacts, r *SplitSupersededReserveFacts) { r.Children = r.Children[:1] }},
		{"changed child", func(f *SplitReserveTransferFacts, _ *SplitSupersededReserveFacts) {
			f.Child.Status = Present("cancelled")
		}},
		{"foreign native changed", func(f *SplitReserveTransferFacts, r *SplitSupersededReserveFacts) {
			f.ParentTarget.Positions[1].Reserve = Present(Decimal("0"))
			r.Target = f.ParentTarget
		}},
		{"remaining owned reserve", func(f *SplitReserveTransferFacts, r *SplitSupersededReserveFacts) {
			f.ParentTarget.Positions[0].Reserve = Present(Decimal("1"))
			r.Target = f.ParentTarget
		}},
		{"warehouse mapping changed", func(f *SplitReserveTransferFacts, _ *SplitSupersededReserveFacts) {
			f.ParentRelease.Command.Warehouse.Version++
		}},
		{"mapping changed", func(f *SplitReserveTransferFacts, _ *SplitSupersededReserveFacts) {
			f.ParentRelease.Command.CustomerOrder.Positions[0].Mapping.MoyskladID = "foreign-product"
		}},
		{"target revision substituted", func(_ *SplitReserveTransferFacts, r *SplitSupersededReserveFacts) { r.Target.Revision = "different" }},
	}
}

func TestSupersededTransferRejectsPartiallyReleasedMultipleOwnedLines(t *testing.T) {
	t.Parallel()
	_, retirement := supersededTransferFixture(t)
	baseline := retirement.Baseline
	second := baseline.Bindings[0]
	second.Source.Identity.ProviderLineID = "second-owned-line"
	second.Native.ID = "second-owned-native"
	second.Source.Reserve, second.Native.Reserve = Present(Decimal("1")), Present(Decimal("1"))
	baseline.Bindings = append(slices.Clone(baseline.Bindings), second)
	if baseline.Validate() != nil {
		t.Fatal("invalid multi-line fixture")
	}
	if supersededTransferAllReleased(baseline) {
		t.Fatal("one remaining owned reserve unlocked children")
	}
	baseline.Bindings[1].Source.Reserve, baseline.Bindings[1].Native.Reserve = Present(Decimal("0")), Present(Decimal("0"))
	if !supersededTransferAllReleased(baseline) {
		t.Fatal("entire owned frontier remained blocked")
	}
}
