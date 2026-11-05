package orders

import (
	"slices"
	"testing"
	"time"
)

func retainedAdmissionFixture(t *testing.T) RetainedSplitAdmissionFacts {
	t.Helper()
	original := numericSplitFixture(t, splitTransferFixture(t))
	acknowledgeUnassembledFixture(&original.Split)
	binding, err := CorrelationFor(original.Before.Scope, original.Before.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	original.Split.Command.Correlation = binding
	original.Parent.Status, original.Child.Status = Present("awaiting_packaging"), Present("awaiting_packaging")
	command := numericSplitFixture(t, testCommand(t))
	command.Correlation = binding
	command.ExpectedSourceRevision = original.Parent.Evidence.Revision
	command.CustomerOrder = original.ParentRelease.Command.CustomerOrder
	original.Split.Command.Warehouse = command.Warehouse
	command.Warehouse.OzonWarehouseID = *original.Before.WarehouseID.Value
	original.Split.Command.Warehouse = command.Warehouse
	command.Action = ActionMirrorCreate
	target := original.ParentTarget
	target.Positions = slices.Clone(target.Positions[:1])
	target.Fields = *command.CustomerOrder
	admission := Work{ID: "post-split-create", Scope: binding.Scope, State: StateReconciled, DispatchCount: 1, Command: command, Receipt: Receipt{State: StateAcknowledged, CommandID: command.ID, HTTPStatus: 200, ExternalID: target.Correlation.CustomerOrderID, ObservedAt: target.ObservedAt}}
	now := time.Unix(300, 0).UTC()
	target.ObservedAt = now.Add(3 * time.Second)
	admission.Receipt.ObservedAt = now.Add(2500 * time.Millisecond)
	return RetainedSplitAdmissionFacts{Split: original.Split, Before: original.Before, Parent: original.Parent, Child: original.Child, Children: original.Split.Receipt.ObservedPostings, OriginalCorrelation: binding, AdmissionSource: original.Parent, Admission: admission, Target: target, SplitReconciledAt: now, NativeCreatedAt: now.Add(time.Second), NativeDispatchedAt: now.Add(2 * time.Second), NativeReconciledAt: now.Add(3 * time.Second), SoleNativeAdmission: true}
}

func TestRetainedSplitAdmissionProvesOnlyReducedNativeRoot(t *testing.T) {
	t.Parallel()
	facts := retainedAdmissionFixture(t)
	proof, err := NewRetainedSplitAdmission(facts)
	if err != nil || !proof.Allows(facts.Child) || proof.ParentWorkID != facts.Admission.ID {
		t.Fatalf("split=%v native=%v command=%v err=%v", retainedAdmissionSplit(facts), retainedAdmissionNative(facts), facts.Admission.Command.Validate(), err)
	}
	if !proof.ParentTargetMatches(facts.Target) || !proof.ParentSourceMatches(facts.Parent) {
		t.Fatal("created frontier cannot be checked again")
	}
}

func TestRetainedSplitAdmissionRejectsUnsafeHistoryAndFrontiers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		alter func(*RetainedSplitAdmissionFacts)
	}{
		{"stale target before dispatch", func(f *RetainedSplitAdmissionFacts) { f.Target.ObservedAt = f.NativeDispatchedAt.Add(-time.Minute) }},
		{"stale target before acknowledgment", func(f *RetainedSplitAdmissionFacts) {
			f.Target.ObservedAt = f.Admission.Receipt.ObservedAt.Add(-time.Millisecond)
		}},
		{"known product substitution", func(f *RetainedSplitAdmissionFacts) {
			f.Before.Lines[0].ProductID = Present("known-original")
			for i := range f.Split.Receipt.ObservedPostings {
				f.Split.Receipt.ObservedPostings[i].Lines[0].ProductID = Present("different-product")
			}
		}},
		{"queued", func(f *RetainedSplitAdmissionFacts) { f.Admission.State = StateQueued }},
		{"unknown", func(f *RetainedSplitAdmissionFacts) { f.Admission.State = StateUnknown }},
		{"lost acknowledgment", func(f *RetainedSplitAdmissionFacts) { f.Admission.Receipt.State = StateUnknown }},
		{"earlier native history", func(f *RetainedSplitAdmissionFacts) { f.SoleNativeAdmission = false }},
		{"before split", func(f *RetainedSplitAdmissionFacts) { f.NativeCreatedAt = f.SplitReconciledAt.Add(-time.Second) }},
		{"native wrong scope", func(f *RetainedSplitAdmissionFacts) { f.Target.Scope.MoyskladAccount = "foreign" }},
		{"original full quantity", func(f *RetainedSplitAdmissionFacts) { f.Admission.Command.CustomerOrder.Positions[0].Quantity = "4" }},
		{"original full reserve", func(f *RetainedSplitAdmissionFacts) { f.Target.Positions[0].Reserve = Present(Decimal("4")) }},
		{"shipped root", func(f *RetainedSplitAdmissionFacts) { f.Target.Positions[0].Shipped = Present(Decimal("1")) }},
		{"deleted root", func(f *RetainedSplitAdmissionFacts) { f.Target.SourceDeleted = Present("deleted") }},
		{"missing member", func(f *RetainedSplitAdmissionFacts) { f.Children = f.Children[:1] }},
		{"native UUID substitution", func(f *RetainedSplitAdmissionFacts) { f.Admission.Receipt.ExternalID = "other-native" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts := retainedAdmissionFixture(t)
			tc.alter(&facts)
			if _, err := NewRetainedSplitAdmission(facts); err == nil {
				t.Fatal("unsafe admission accepted")
			}
		})
	}
}
