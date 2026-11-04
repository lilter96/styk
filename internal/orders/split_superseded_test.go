package orders

import (
	"slices"
	"testing"
)

func supersededSplitFixture(t *testing.T) (SplitSupersededReserveFacts, Command) {
	t.Helper()
	base := splitTransferFixture(t)
	baseline := base.ParentRelease.Command.PositionPatch.Baseline
	base.Split.Command.Warehouse = testCommand(t).Warehouse
	base.Split.Command.Warehouse.OzonWarehouseID = *base.Before.WarehouseID.Value
	replacement := base.Split.Receipt.ObservedPostings[0]
	replacement.PostingNumber = "replacement-a"
	replacement.ChildPostingNumbers = nil
	replacement.ParentPostingNumber = Present(base.Before.PostingNumber)
	base.Split.Receipt.PostingNumbers = []string{replacement.PostingNumber, base.Child.PostingNumber}
	base.Split.Receipt.ObservedPostings = []Posting{replacement, base.Child}
	parent := base.Before
	parent.Status = Present(SupersededSplitParentStatus)
	parent.Evidence.Revision = "positive-cancelled-from-split"
	command := testCommand(t)
	command.Action = ActionMirrorUpdate
	command.Scope, command.Correlation = baseline.Scope, baseline.Correlation
	command.CustomerOrder = &CustomerOrderWrite{OrganizationID: "org", AgentID: "agent", StoreID: base.Split.Command.Warehouse.MoyskladStoreID, Applicable: Present(true), Name: Present("unchanged-merchant-name"), Positions: []PositionWrite{baseline.Bindings[0].Source}}
	command.ExpectedSourceRevision = parent.Evidence.Revision
	command.ExpectedTargetRevision = "original-native-frontier"
	command.TargetPositionsComplete = true
	command.TargetPositions = []TargetPosition{baseline.Bindings[0].Native, base.ParentTarget.Positions[1]}
	target := TargetObservation{Scope: baseline.Scope, Correlation: baseline.Correlation, Revision: command.ExpectedTargetRevision, ObservedAt: base.ParentTarget.ObservedAt, Complete: true, Fields: *command.CustomerOrder, Positions: slices.Clone(command.TargetPositions)}
	return SplitSupersededReserveFacts{Split: base.Split, Before: base.Before, OriginalCorrelation: baseline.Correlation, Parent: parent, Children: base.Split.Receipt.ObservedPostings, Baseline: baseline, Target: target}, command
}

func TestSupersededSplitPositiveStatusPlansOnlyOwnedReserveZero(t *testing.T) {
	t.Parallel()
	facts, command := supersededSplitFixture(t)
	proof, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanSupersededSplitReserve(command, proof)
	if err != nil || plan.PositionPatch == nil {
		t.Fatal("reserve-only native plan unavailable", err)
	}
	patch := plan.PositionPatch
	if patch.Quantity.IsSpecified() || patch.Price.IsSpecified() || !patch.Reserve.IsKnown() || *patch.Reserve.Value != "0" || patch.PositionID != facts.Baseline.Bindings[0].Native.ID {
		t.Fatal("superseded release changed native accounting quantity/price")
	}
	if plan.CustomerOrder.Positions[0].Quantity != "4" || *plan.CustomerOrder.Positions[0].Reserve.Value != "0" || !*plan.CustomerOrder.Applicable.Value || *plan.CustomerOrder.Name.Value != "unchanged-merchant-name" {
		t.Fatal("release rewrote historical document or application state")
	}
	command.Action, command.CustomerOrder, command.PositionPatch = ActionMirrorPositionUpdate, &plan.CustomerOrder, patch
	target := facts.Target
	target.Positions = slices.Clone(target.Positions)
	target.Positions[0].Reserve = Present(Decimal("0"))
	if !PositionPatchObserved(command, target) {
		t.Fatal("owned reserve-only update did not reconcile")
	}
	target.Positions[1].Reserve = Present(Decimal("0"))
	if PositionPatchObserved(command, target) {
		t.Fatal("foreign reserve released")
	}
	if *facts.Baseline.Bindings[0].Source.Reserve.Value != "4" || *facts.Target.Positions[1].Reserve.Value != "2" {
		t.Fatal("immutable original facts changed")
	}
}

func TestSupersededSplitProofRejectsUnsupportedOrIncompleteFacts(t *testing.T) {
	t.Parallel()
	cases := append(supersededSplitSourceCases(), supersededSplitNativeCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts, _ := supersededSplitFixture(t)
			tc.alter(&facts)
			if _, err := NewSupersededSplitReserve(facts); err == nil {
				t.Fatal("unsupported superseded release accepted")
			}
		})
	}
}

type supersededMutation struct {
	name  string
	alter func(*SplitSupersededReserveFacts)
}

func supersededSplitSourceCases() []supersededMutation {
	return []supersededMutation{
		{"ordinary cancellation is different", func(f *SplitSupersededReserveFacts) { f.Parent.Status = Present("cancelled") }},
		{"missing parent not absence proof", func(f *SplitSupersededReserveFacts) { f.Parent = Posting{} }},
		{"unknown provider status", func(f *SplitSupersededReserveFacts) { f.Parent.Status = Present("future_cancelled_from_split") }},
		{"missing status", func(f *SplitSupersededReserveFacts) { f.Parent.Status = Field[string]{} }},
		{"unknown split result", func(f *SplitSupersededReserveFacts) { f.Split.State = StateUnknown }},
		{"lost acknowledgement", func(f *SplitSupersededReserveFacts) { f.Split.Receipt.HTTPStatus = 0 }},
		{"receipt still acknowledged only", func(f *SplitSupersededReserveFacts) { f.Split.Receipt.State = StateAcknowledged }},
		{"partial observed child array", func(f *SplitSupersededReserveFacts) { f.Children = f.Children[:1] }},
		{"child quantity changed", func(f *SplitSupersededReserveFacts) {
			f.Children = slices.Clone(f.Children)
			f.Children[0].Lines = slices.Clone(f.Children[0].Lines)
			f.Children[0].Lines[0].Quantity = "1"
		}},
		{"retained parent case", func(f *SplitSupersededReserveFacts) { f.Split.Receipt.PostingNumbers[0] = f.Before.PostingNumber }},
		{"provider ancestor", func(f *SplitSupersededReserveFacts) { f.Before.ParentPostingNumber = Present("earlier-parent") }},
		{"known nested child", func(f *SplitSupersededReserveFacts) {
			f.Children = slices.Clone(f.Children)
			f.Children[0].ChildPostingNumbers = []string{"nested"}
		}},
		{"different commercial order", func(f *SplitSupersededReserveFacts) { f.Parent.CommercialOrderID = "other-order" }},
		{"shipment already observed", func(f *SplitSupersededReserveFacts) {
			f.Parent.Cancellation = Present(Cancellation{AfterShipment: Present(true)})
		}},
	}
}

func supersededSplitNativeCases() []supersededMutation {
	return []supersededMutation{
		{"foreign tenant", func(f *SplitSupersededReserveFacts) { f.Baseline.Scope.Tenant = "other" }},
		{"foreign native order", func(f *SplitSupersededReserveFacts) { f.Target.Correlation.CustomerOrderID = "other-native-order" }},
		{"foreign warehouse", func(f *SplitSupersededReserveFacts) { f.Target.Fields.StoreID = "other-native-store" }},
		{"unposted target", func(f *SplitSupersededReserveFacts) { f.Target.Fields.Applicable = Present(false) }},
		{"reserve exceeds owned quantity", func(f *SplitSupersededReserveFacts) {
			f.Baseline.Bindings[0].Source.Reserve = Present(Decimal("5"))
			f.Baseline.Bindings[0].Native.Reserve = Present(Decimal("5"))
			f.Target.Positions[0].Reserve = Present(Decimal("5"))
		}},
		{"unknown native reserve", func(f *SplitSupersededReserveFacts) { f.Target.Positions[0].Reserve = Field[Decimal]{} }},
		{"foreign reserve modified", func(f *SplitSupersededReserveFacts) { f.Target.Positions[0].Reserve = Present(Decimal("1")) }},
		{"unknown shipped", func(f *SplitSupersededReserveFacts) { f.Baseline.Bindings[0].Native.Shipped = Field[Decimal]{} }},
		{"already shipped", func(f *SplitSupersededReserveFacts) { f.Baseline.Bindings[0].Native.Shipped = Present(Decimal("1")) }},
		{"operator asserted ownership", func(f *SplitSupersededReserveFacts) { f.Baseline.EvidenceClass = "OPERATOR_ATTESTED" }},
		{"native quantity changed", func(f *SplitSupersededReserveFacts) { f.Target.Positions[0].Quantity = "2" }},
		{"source price changed", func(f *SplitSupersededReserveFacts) {
			f.Before.Lines = slices.Clone(f.Before.Lines)
			f.Before.Lines[0].Price = Present(Money{Amount: "999", Currency: "RUB"})
		}},
		{"source product changed", func(f *SplitSupersededReserveFacts) {
			f.Before.Lines = slices.Clone(f.Before.Lines)
			f.Before.Lines[0].ProductID = Present("different-product")
		}},
	}
}

func TestSupersededSplitReleaseRejectsStaleCommandAndPreservesPrecision(t *testing.T) {
	t.Parallel()
	facts, command := supersededSplitFixture(t)
	proof, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal(err)
	}
	command.ExpectedSourceRevision = "different-source"
	if _, err = PlanSupersededSplitReserve(command, proof); err == nil {
		t.Fatal("stale source plan accepted")
	}
	command.ExpectedSourceRevision = facts.Parent.Evidence.Revision
	command.TargetPositions = slices.Clone(command.TargetPositions)
	command.TargetPositions[1].Reserve = Present(Decimal("1"))
	if _, err = PlanSupersededSplitReserve(command, proof); err == nil {
		t.Fatal("changed foreign native frontier accepted")
	}
}

func TestSupersededSplitReleasePreservesNativeDecimalRepresentations(t *testing.T) {
	t.Parallel()
	facts, command := supersededSplitFixture(t)
	facts.Baseline.Bindings[0].Source.Quantity = "4.000"
	facts.Baseline.Bindings[0].Source.Reserve = Present(Decimal("4.00"))
	facts.Baseline.Bindings[0].Native.Quantity = "4.0"
	facts.Baseline.Bindings[0].Native.Reserve = Present(Decimal("4.000"))
	facts.Target.Positions[0] = facts.Baseline.Bindings[0].Native
	command.CustomerOrder.Positions[0] = facts.Baseline.Bindings[0].Source
	command.TargetPositions = slices.Clone(facts.Target.Positions)
	proof, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanSupersededSplitReserve(command, proof)
	if err != nil || plan.PositionPatch == nil || plan.PositionPatch.Quantity.IsSpecified() || plan.CustomerOrder.Positions[0].Quantity != "4.000" || *plan.PositionPatch.Reserve.Value != "0" {
		t.Fatal("decimal variant caused accounting rewrite", err)
	}
}

func supersededSplitTwoLineFixture(t *testing.T) (SplitSupersededReserveFacts, Command) {
	t.Helper()
	facts, command := supersededSplitFixture(t)
	binding := facts.Baseline.Bindings[0]
	binding.Source.Identity = LineIdentity{SKU: "sku-second", OfferID: "offer-second"}
	binding.Source.Mapping.SKU, binding.Source.Mapping.OfferID, binding.Source.Mapping.OzonProductID, binding.Source.Mapping.MoyskladID = "sku-second", "offer-second", "ozon-second", "moy-second"
	binding.Native.ID, binding.Native.Identity, binding.Native.MoyskladID = "second-native-position", binding.Source.Identity, binding.Source.Mapping.MoyskladID
	facts.Before.Lines = append(facts.Before.Lines, PostingLine{Identity: binding.Source.Identity, Quantity: "4"})
	facts.Baseline.Bindings = append(facts.Baseline.Bindings, binding)
	facts.Target.Positions = append(facts.Target.Positions, binding.Native)
	command.TargetPositions = slices.Clone(facts.Target.Positions)
	command.CustomerOrder.Positions = append(command.CustomerOrder.Positions, binding.Source)
	for i := range facts.Children {
		facts.Children[i].Lines = append(slices.Clone(facts.Children[i].Lines), PostingLine{Identity: binding.Source.Identity, Quantity: "2"})
	}
	facts.Split.Receipt.ObservedPostings = facts.Children
	for i := range facts.Split.Command.PostingAction.Parameters.Packages {
		facts.Split.Command.PostingAction.Parameters.Packages[i].Lines = append(facts.Split.Command.PostingAction.Parameters.Packages[i].Lines, ActionQuantity{Identity: binding.Source.Identity, Quantity: "2"})
	}
	return facts, command
}

func TestSupersededSplitReleaseProgressIsOneExactNativePositionPerDecision(t *testing.T) {
	t.Parallel()
	facts, command := supersededSplitTwoLineFixture(t)
	proof, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanSupersededSplitReserve(command, proof)
	if err != nil || plan.PositionPatch == nil {
		t.Fatal(err)
	}
	if *plan.CustomerOrder.Positions[0].Reserve.Value != "0" || *plan.CustomerOrder.Positions[1].Reserve.Value != "4" {
		t.Fatal("first one-position write falsely claimed every reserve released")
	}
	target := facts.Target
	target.Positions = slices.Clone(target.Positions)
	target.Positions[0].Reserve = Present(Decimal("0"))
	target.Revision = "after-first-release"
	command.Action, command.CustomerOrder, command.PositionPatch = ActionMirrorPositionUpdate, &plan.CustomerOrder, plan.PositionPatch
	baseline, err := ProjectionPositionBaseline(Work{ID: "actual-first-release", Scope: command.Scope, State: StateReconciled, DispatchCount: 1, Command: command}, target)
	if err != nil {
		t.Fatal(err)
	}
	facts.Baseline, facts.Target = baseline, target
	next, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal(err)
	}
	command.ExpectedTargetRevision, command.TargetPositions = target.Revision, slices.Clone(target.Positions)
	second, err := PlanSupersededSplitReserve(command, next)
	if err != nil || second.PositionPatch == nil || second.PositionPatch.PositionID != "second-native-position" || *second.CustomerOrder.Positions[0].Reserve.Value != "0" || *second.CustomerOrder.Positions[1].Reserve.Value != "0" {
		t.Fatal("next exact owned reserve not released truthfully", err)
	}
}

func TestSupersededSplitReleaseAlreadyZeroIsTruthfulNoOp(t *testing.T) {
	t.Parallel()
	facts, command := supersededSplitFixture(t)
	facts.Baseline.Bindings[0].Source.Reserve = Present(Decimal("0"))
	facts.Baseline.Bindings[0].Native.Reserve = Present(Decimal("0"))
	facts.Target.Positions[0].Reserve = Present(Decimal("0"))
	command.TargetPositions = slices.Clone(facts.Target.Positions)
	command.CustomerOrder.Positions[0].Reserve = Present(Decimal("0"))
	proof, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanSupersededSplitReserve(command, proof)
	if err != nil || plan.PositionPatch != nil || len(plan.CustomerOrder.OwnedFields) != 0 {
		t.Fatal("already released reserve generated another mutation", err)
	}
	command.Action = ActionMirrorCreate
	if _, err = PlanSupersededSplitReserve(command, proof); err == nil {
		t.Fatal("release permission creates another customerorder")
	}
}

func TestSupersededSplitPermissionFreezesTypedFactsWithoutBuyerData(t *testing.T) {
	t.Parallel()
	facts, _ := supersededSplitFixture(t)
	facts.Parent.PII = &Customer{Name: Present("private")}
	facts.Parent.RelatedPostings = &RelatedPostingGroup{Presence: PresenceValue, PostingNumbers: Present([]string{"z", "a"})}
	proof, err := NewSupersededSplitReserve(facts)
	if err != nil {
		t.Fatal(err)
	}
	*facts.Parent.Status.Value = "later-status"
	*facts.Baseline.Bindings[0].Source.Reserve.Value = "3"
	*facts.Target.Positions[0].Reserve.Value = "1"
	(*facts.Parent.RelatedPostings.PostingNumbers.Value)[0] = "changed"
	if proof.ParentPosting.PII != nil || *proof.ParentPosting.Status.Value != SupersededSplitParentStatus || *proof.Baseline.Bindings[0].Source.Reserve.Value != "4" || *proof.ParentPositions[0].Reserve.Value != "4" || (*proof.ParentPosting.RelatedPostings.PostingNumbers.Value)[0] != "a" {
		t.Fatal("permission retained mutable pointers or buyer data")
	}
}
