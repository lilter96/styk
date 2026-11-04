package orders

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func splitTransferFixture(t *testing.T) SplitReserveTransferFacts {
	t.Helper()
	parent, write, baseline, retained := retainedSplitFixture(t)
	before := retained.Before
	before.WarehouseID, before.DeliveryMethodID = Present("warehouse"), Present("delivery")
	parent.WarehouseID, parent.DeliveryMethodID = before.WarehouseID, before.DeliveryMethodID
	parent.Status = Present("awaiting_deliver")
	retained.Before = before
	if invalid := ApplyRetainedSplitOpenQuantityReserve(parent, &write, baseline, retained); invalid.Code != "" {
		t.Fatal(invalid)
	}
	command := testCommand(t)
	command.Correlation, command.CustomerOrder = baseline.Correlation, &write
	command.RetainedSplitWorkID = retained.SplitWorkID
	command.ExpectedSourceRevision = parent.Evidence.Revision
	command.ExpectedTargetRevision = "native-before-reduction"
	command.TargetPositions = []TargetPosition{baseline.Bindings[0].Native, {ID: "foreign-position", MoyskladKind: "product", MoyskladID: "foreign-item", Quantity: "7", Reserve: Present(Decimal("2")), Shipped: Present(Decimal("0"))}}
	command.TargetPositionsComplete = true
	patch, err := PlanPositionPatch(command, baseline)
	if err != nil || patch == nil {
		t.Fatal("plan owned reduction", err)
	}
	command.Action, command.PositionPatch = ActionMirrorPositionUpdate, patch
	command.CustomerOrder.OwnedFields = patch.Fields()
	target := TargetObservation{Scope: command.Scope, Correlation: baseline.Correlation, Revision: "native-after-reduction", Complete: true, ObservedAt: time.Unix(200, 0).UTC(), Fields: write, Positions: slices.Clone(command.TargetPositions)}
	target.Positions[0].Quantity, target.Positions[0].Reserve = "2", Present(Decimal("2"))
	release := Work{ID: "parent-release", Scope: command.Scope, Command: command, DispatchCount: 1, State: StateReconciled, Receipt: Receipt{State: StateAcknowledged, HTTPStatus: 200, CommandID: command.ID, ExternalID: baseline.Correlation.CustomerOrderID}}
	child := parent
	child.PostingNumber, child.ChildPostingNumbers = "actual-child", nil
	child.Evidence.Revision = "actual-child-revision"
	child.ParentPostingNumber = Present(parent.PostingNumber)
	split := splitTransferWork(before, parent, child, baseline.Correlation, retained.SplitWorkID)
	return SplitReserveTransferFacts{Split: split, Before: before, OriginalCorrelation: baseline.Correlation, ParentRelease: release, Parent: parent, ParentTarget: target, Child: child}
}

func splitTransferWork(before, parent, child Posting, correlation Correlation, id string) Work {
	line := ActionQuantity{Identity: before.Lines[0].Identity, Quantity: "2"}
	command := Command{ID: "split-command", Scope: before.Scope, Action: ActionPostingSplit, PostingNumber: before.PostingNumber, CommercialOrderID: before.CommercialOrderID, ExpectedSourceRevision: before.Evidence.Revision, Correlation: correlation, PostingAction: &PostingAction{Parameters: ActionParameters{Packages: []ActionPackage{{Lines: []ActionQuantity{line}}, {Lines: []ActionQuantity{line}}}}}}
	return Work{ID: id, Scope: before.Scope, Command: command, DispatchCount: 1, State: StateReconciled, Receipt: Receipt{CommandID: command.ID, State: StateReconciled, HTTPStatus: 200, PostingNumbers: []string{parent.PostingNumber, child.PostingNumber}, ObservedPostings: []Posting{parent, child}}}
}

func TestSplitTransferUnlocksOnlyChildAfterEntireOwnedParentReduction(t *testing.T) {
	t.Parallel()
	facts := splitTransferFixture(t)
	proof, err := NewSplitReserveTransfer(facts)
	if err != nil || !proof.Allows(facts.Child) {
		t.Fatalf("complete transfer proof blocked: %+v %v", proof, err)
	}
	write := *facts.ParentRelease.Command.CustomerOrder
	write.Positions = slices.Clone(write.Positions)
	write.Positions[0].Reserve = Field[Decimal]{}
	if invalid := ApplyTransferredSplitOpenQuantityReserve(facts.Child, &write, PositionBaseline{}, proof); invalid.Code != "" {
		t.Fatal(invalid)
	}
	if *write.Positions[0].Reserve.Value != "2" || proof.ParentWorkID != facts.ParentRelease.ID || len(proof.ParentPositions) != 2 || *proof.ParentPositions[1].Reserve.Value != "2" {
		t.Fatal("transfer quantities or independent reserve changed")
	}
	if proof.Allows(facts.Parent) {
		t.Fatal("child permission also authorized parent")
	}
}

func TestSplitTransferRejectsInvalidProofFacts(t *testing.T) {
	t.Parallel()
	cases := append(splitTransferRootCases(), splitTransferReleaseCases()...)
	cases = append(cases, splitTransferObservationCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts := splitTransferFixture(t)
			tc.alter(&facts)
			if _, err := NewSplitReserveTransfer(facts); err == nil {
				t.Fatal("incomplete or substituted transfer accepted")
			}
		})
	}
}

type splitTransferMutation struct {
	name  string
	alter func(*SplitReserveTransferFacts)
}

func splitTransferRootCases() []splitTransferMutation {
	return []splitTransferMutation{
		{"split unknown", func(f *SplitReserveTransferFacts) { f.Split.State = StateUnknown }},
		{"split no dispatch", func(f *SplitReserveTransferFacts) { f.Split.DispatchCount = 0 }},
		{"wrong account", func(f *SplitReserveTransferFacts) { f.Split.Scope.OzonAccount = "foreign" }},
		{"wrong correlation", func(f *SplitReserveTransferFacts) { f.OriginalCorrelation.CustomerOrderID = "foreign" }},
		{"wrong frozen revision", func(f *SplitReserveTransferFacts) { f.Split.Command.ExpectedSourceRevision = "changed" }},
		{"provider ancestry", func(f *SplitReserveTransferFacts) { f.Before.ParentPostingNumber = Present("ancestor") }},
		{"prior source children", func(f *SplitReserveTransferFacts) { f.Before.ChildPostingNumbers = []string{"previous-child"} }},
		{"missing retained output", func(f *SplitReserveTransferFacts) { f.Split.Receipt.PostingNumbers[0] = "different" }},
		{"partial output observations", func(f *SplitReserveTransferFacts) {
			f.Split.Receipt.ObservedPostings = f.Split.Receipt.ObservedPostings[:1]
		}},
		{"wrong package quantity", func(f *SplitReserveTransferFacts) {
			f.Split.Command.PostingAction.Parameters.Packages[0].Lines[0].Quantity = "3"
		}},
	}
}

func splitTransferReleaseCases() []splitTransferMutation {
	return []splitTransferMutation{
		{"parent unknown", func(f *SplitReserveTransferFacts) { f.ParentRelease.State = StateUnknown }},
		{"parent unacknowledged", func(f *SplitReserveTransferFacts) { f.ParentRelease.Receipt.State = StateUnknown }},
		{"parent rejected HTTP", func(f *SplitReserveTransferFacts) { f.ParentRelease.Receipt.HTTPStatus = 403 }},
		{"parent wrong acknowledged target", func(f *SplitReserveTransferFacts) { f.ParentRelease.Receipt.ExternalID = "foreign" }},
		{"parent never dispatched", func(f *SplitReserveTransferFacts) { f.ParentRelease.DispatchCount = 0 }},
		{"different split release", func(f *SplitReserveTransferFacts) { f.ParentRelease.Command.RetainedSplitWorkID = "other-split" }},
		{"wrong parent source", func(f *SplitReserveTransferFacts) { f.ParentRelease.Command.ExpectedSourceRevision = "old-source" }},
		{"wrong native store", func(f *SplitReserveTransferFacts) { f.ParentTarget.Fields.StoreID = "foreign-store" }},
		{"wrong native organization", func(f *SplitReserveTransferFacts) { f.ParentTarget.Fields.OrganizationID = "foreign-org" }},
		{"scalar mirror cannot prove release", func(f *SplitReserveTransferFacts) {
			f.ParentRelease.Command.Action = ActionMirrorUpdate
			f.ParentRelease.Command.PositionPatch = nil
		}},
	}
}

func splitTransferObservationCases() []splitTransferMutation {
	return []splitTransferMutation{
		{"parent quantity unchanged", func(f *SplitReserveTransferFacts) { f.ParentTarget.Positions[0].Quantity = "4" }},
		{"parent still reserved", func(f *SplitReserveTransferFacts) { f.ParentTarget.Positions[0].Reserve = Present(Decimal("4")) }},
		{"foreign reserve changed", func(f *SplitReserveTransferFacts) { f.ParentTarget.Positions[1].Reserve = Present(Decimal("1")) }},
		{"native parent unposted", func(f *SplitReserveTransferFacts) { f.ParentTarget.Fields.Applicable = Present(false) }},
		{"native shipped", func(f *SplitReserveTransferFacts) { f.ParentTarget.Positions[0].Shipped = Present(Decimal("1")) }},
		{"native partial observation", func(f *SplitReserveTransferFacts) { f.ParentTarget.Complete = false }},
		{"retained actual allocation changed", func(f *SplitReserveTransferFacts) {
			f.Parent.Lines = slices.Clone(f.Parent.Lines)
			f.Parent.Lines[0].Quantity = "1"
		}},
		{"child actual allocation changed", func(f *SplitReserveTransferFacts) {
			f.Child.Lines = slices.Clone(f.Child.Lines)
			f.Child.Lines[0].Quantity = "1"
		}},
		{"child changed identity", func(f *SplitReserveTransferFacts) {
			f.Child.Lines = slices.Clone(f.Child.Lines)
			f.Child.Lines[0].Identity.SKU = "other"
		}},
		{"child product identity changed", func(f *SplitReserveTransferFacts) {
			f.Split.Receipt.ObservedPostings[1].Lines = slices.Clone(f.Split.Receipt.ObservedPostings[1].Lines)
			f.Split.Receipt.ObservedPostings[1].Lines[0].ProductID = Present("original-product")
			f.Child.Lines = slices.Clone(f.Child.Lines)
			f.Child.Lines[0].ProductID = Present("other-product")
		}},
		{"nested child", func(f *SplitReserveTransferFacts) { f.Child.ChildPostingNumbers = []string{"grandchild"} }},
		{"delivered child", func(f *SplitReserveTransferFacts) { f.Child.Status = Present("delivered") }},
	}
}

func TestSplitTransferPermissionFreshnessAndNativeBusinessScope(t *testing.T) {
	t.Parallel()
	facts := splitTransferFixture(t)
	proof, err := NewSplitReserveTransfer(facts)
	if err != nil {
		t.Fatal(err)
	}
	refreshed := proof
	refreshed.ChildPosting.Evidence.CheckedAt = time.Unix(300, 0)
	refreshed.ChildPosting.Evidence.ObservedAt = time.Unix(300, 0)
	if !proof.Equal(refreshed) || !proof.Allows(refreshed.ChildPosting) {
		t.Fatal("identical fresh traversal invalidated facts")
	}
	changed := proof
	changed.ChildPosting.Evidence.Revision = "different-source"
	if proof.Equal(changed) || proof.Allows(changed.ChildPosting) {
		t.Fatal("source revision drift ignored")
	}
	write := *facts.ParentRelease.Command.CustomerOrder
	write.StoreID = "other-store"
	if invalid := ApplyTransferredSplitOpenQuantityReserve(facts.Child, &write, PositionBaseline{}, proof); invalid.Code == "" {
		t.Fatal("child reserve moved to another native store")
	}
	write.StoreID = proof.ParentStoreID
	write.OrganizationID = "other-org"
	if invalid := ApplyTransferredSplitOpenQuantityReserve(facts.Child, &write, PositionBaseline{}, proof); invalid.Code == "" {
		t.Fatal("child reserve moved to another organization")
	}
}

func TestSplitTransferPermissionDoesNotCopyCustomerData(t *testing.T) {
	t.Parallel()
	facts := splitTransferFixture(t)
	facts.Parent.PII = &Customer{Name: Present("private-marker")}
	facts.Child.PII = &Customer{Name: Present("private-marker")}
	proof, err := NewSplitReserveTransfer(facts)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	if proof.ParentPosting.PII != nil || proof.ChildPosting.PII != nil || strings.Contains(string(raw), "private-marker") {
		t.Fatal("transfer proof copied transient customer data")
	}
}

func TestSplitTransferRequiresEveryParentLineToFinishReduction(t *testing.T) {
	t.Parallel()
	facts := splitTransferTwoLineFixture(t)
	if _, err := NewSplitReserveTransfer(facts); err == nil {
		t.Fatal("first patch unlocked child before second line reserve reduced")
	}
	baseline, err := ProjectionPositionBaseline(facts.ParentRelease, facts.ParentTarget)
	if err != nil {
		t.Fatal(err)
	}
	command := facts.ParentRelease.Command
	command.ID = "release-second-line"
	command.TargetPositions = slices.Clone(facts.ParentTarget.Positions)
	command.ExpectedTargetRevision = facts.ParentTarget.Revision
	patch, err := PlanPositionPatch(command, baseline)
	if err != nil || patch == nil || patch.PositionID != "second-native-position" {
		t.Fatal("second owned patch", err)
	}
	command.PositionPatch = patch
	command.CustomerOrder.OwnedFields = patch.Fields()
	facts.ParentRelease.ID, facts.ParentRelease.Command = "second-parent-release", command
	facts.ParentRelease.Receipt.CommandID = command.ID
	facts.ParentTarget.Positions[2].Quantity = "2"
	facts.ParentTarget.Positions[2].Reserve = Present(Decimal("2"))
	facts.ParentTarget.Revision = "all-native-lines-reduced"
	proof, err := NewSplitReserveTransfer(facts)
	if err != nil || !proof.Allows(facts.Child) {
		t.Fatal("all exact parent lines reduced but child blocked", err)
	}
}

func splitTransferTwoLineFixture(t *testing.T) SplitReserveTransferFacts {
	t.Helper()
	facts := splitTransferFixture(t)
	source := facts.ParentRelease.Command.PositionPatch.Baseline.Bindings[0].Source
	source.Identity = LineIdentity{SKU: "sku-second", OfferID: "offer-second"}
	source.Mapping.SKU, source.Mapping.OfferID, source.Mapping.OzonProductID, source.Mapping.MoyskladID = "sku-second", "offer-second", "second-ozon-product", "second-moy-product"
	native := facts.ParentRelease.Command.PositionPatch.Baseline.Bindings[0].Native
	native.ID, native.Identity, native.MoyskladID = "second-native-position", source.Identity, source.Mapping.MoyskladID
	facts.ParentRelease.Command.PositionPatch.Baseline.Bindings = append(facts.ParentRelease.Command.PositionPatch.Baseline.Bindings, PositionBinding{Source: source, Native: native})
	facts.ParentRelease.Command.TargetPositions = append(facts.ParentRelease.Command.TargetPositions, native)
	facts.ParentTarget.Positions = append(facts.ParentTarget.Positions, native)
	wanted := source
	wanted.Quantity, wanted.Reserve = "2", Present(Decimal("2"))
	facts.ParentRelease.Command.CustomerOrder.Positions = append(facts.ParentRelease.Command.CustomerOrder.Positions, wanted)
	facts.Before.Lines = append(facts.Before.Lines, PostingLine{Identity: source.Identity, Quantity: "4"})
	facts.Parent.Lines = append(slices.Clone(facts.Parent.Lines), PostingLine{Identity: source.Identity, Quantity: "2"})
	facts.Child.Lines = append(slices.Clone(facts.Child.Lines), PostingLine{Identity: source.Identity, Quantity: "2"})
	for i := range facts.Split.Command.PostingAction.Parameters.Packages {
		facts.Split.Command.PostingAction.Parameters.Packages[i].Lines = append(facts.Split.Command.PostingAction.Parameters.Packages[i].Lines, ActionQuantity{Identity: source.Identity, Quantity: "2"})
	}
	facts.Split.Receipt.ObservedPostings = []Posting{facts.Parent, facts.Child}
	return facts
}

func TestSplitTransferSealedChildCommandCannotUseParentNativeOrderOrMissingReserve(t *testing.T) {
	t.Parallel()
	facts := splitTransferFixture(t)
	proof, err := NewSplitReserveTransfer(facts)
	if err != nil {
		t.Fatal(err)
	}
	command := facts.ParentRelease.Command
	command.PostingNumber, command.ExpectedSourceRevision = facts.Child.PostingNumber, facts.Child.Evidence.Revision
	command.Action = ActionMirrorCreate
	command.Correlation, err = CorrelationFor(facts.Child.Scope, facts.Child.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	if !proof.AllowsCommand(command) {
		t.Fatal("sealed exact child command blocked")
	}
	command.Correlation.CustomerOrderID = proof.ParentCorrelation.CustomerOrderID
	if proof.AllowsCommand(command) {
		t.Fatal("child overwrites original native parent")
	}
	command.Correlation.CustomerOrderID = ""
	command.CustomerOrder.Positions[0].Reserve = Field[Decimal]{}
	if proof.AllowsCommand(command) {
		t.Fatal("missing sealed reserve silently filled")
	}
}

func cancelledRetainedTransferFixture(t *testing.T) SplitReserveTransferFacts {
	t.Helper()
	facts := splitTransferFixture(t)
	facts.Parent.Status = Present("cancelled")
	facts.Parent.Evidence.Revision = "actual-retained-cancellation"
	facts.Parent.Cancellation = Present(Cancellation{AfterShipment: Present(false)})
	facts.ParentRelease.Command.ExpectedSourceRevision = facts.Parent.Evidence.Revision
	facts.ParentRelease.Command.PositionPatch.Reserve = Present(Decimal("0"))
	facts.ParentRelease.Command.CustomerOrder.Positions[0].Reserve = Present(Decimal("0"))
	facts.ParentTarget.Positions[0].Reserve = Present(Decimal("0"))
	return facts
}

func TestSplitTransferAcceptsConfirmedBeforeShipmentCancellationRelease(t *testing.T) {
	t.Parallel()
	facts := cancelledRetainedTransferFixture(t)
	proof, err := NewSplitReserveTransfer(facts)
	if err != nil || !proof.Allows(facts.Child) {
		t.Fatal("known unshipped cancelled parent still blocks independent child", err)
	}
	write := *facts.ParentRelease.Command.CustomerOrder
	write.Positions = slices.Clone(write.Positions)
	write.Positions[0].Reserve = Field[Decimal]{}
	if invalid := ApplyTransferredSplitOpenQuantityReserve(facts.Child, &write, PositionBaseline{}, proof); invalid.Code != "" || !write.Positions[0].Reserve.IsKnown() || *write.Positions[0].Reserve.Value != "2" {
		t.Fatal("child did not retain its independent pending obligation", invalid)
	}
	if *facts.ParentTarget.Positions[0].Reserve.Value != "0" || *facts.ParentTarget.Positions[1].Reserve.Value != "2" {
		t.Fatal("cancelled parent or foreign reserve changed")
	}
}

func TestSplitTransferCancelledParentRequiresExplicitUnshippedEvidence(t *testing.T) {
	t.Parallel()
	cases := []splitTransferMutation{
		{"after shipment", func(f *SplitReserveTransferFacts) {
			f.Parent.Cancellation = Present(Cancellation{AfterShipment: Present(true)})
		}},
		{"missing cancellation", func(f *SplitReserveTransferFacts) { f.Parent.Cancellation = Field[Cancellation]{} }},
		{"null cancellation", func(f *SplitReserveTransferFacts) {
			f.Parent.Cancellation = Field[Cancellation]{Presence: PresenceNull}
		}},
		{"unknown shipment attribution", func(f *SplitReserveTransferFacts) { f.Parent.Cancellation = Present(Cancellation{}) }},
		{"partial cancellation", func(f *SplitReserveTransferFacts) {
			f.Parent.Lines = slices.Clone(f.Parent.Lines)
			f.Parent.Lines[0].CancelledQuantity = Present(Decimal("1"))
		}},
		{"actual physical shipment", func(f *SplitReserveTransferFacts) { f.ParentTarget.Positions[0].Shipped = Present(Decimal("1")) }},
		{"unknown release receipt", func(f *SplitReserveTransferFacts) { f.ParentRelease.Receipt.State = StateUnknown }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts := cancelledRetainedTransferFixture(t)
			tc.alter(&facts)
			if _, err := NewSplitReserveTransfer(facts); err == nil {
				t.Fatal("unknown or shipped cancellation unlocked child")
			}
		})
	}
}
