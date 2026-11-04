package orders

import "testing"

func unmirroredUnassembledFacts(t *testing.T, retained bool) (UnmirroredUnassembledSplitFacts, Posting) {
	t.Helper()
	original := numericSplitFixture(t, splitTransferFixture(t))
	before, parent, work := original.Before, original.Parent, original.Split
	if !retained {
		retired, _ := supersededSplitFixture(t)
		retired = numericSplitFixture(t, retired)
		before, parent, work = retired.Before, retired.Parent, retired.Split
	}
	acknowledgeUnassembledFixture(&work)
	binding, err := CorrelationFor(before.Scope, before.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	work.Command.Correlation = binding
	facts := UnmirroredUnassembledSplitFacts{Split: work, Before: before, Parent: parent, Children: work.Receipt.ObservedPostings, OriginalCorrelation: binding, NoParentNative: true}
	for _, child := range facts.Children {
		if child.PostingNumber != before.PostingNumber {
			return facts, child
		}
	}
	t.Fatal("child fixture missing")
	return facts, Posting{}
}

func TestUnmirroredUnassembledRootNeedsPositiveCompleteServerProof(t *testing.T) {
	t.Parallel()
	for _, retained := range []bool{false, true} {
		facts, child := unmirroredUnassembledFacts(t, retained)
		proof, err := NewUnmirroredUnassembledSplitReserve(facts, child)
		if err != nil || !proof.Allows(child) || proof.Allows(facts.Parent) {
			t.Fatalf("retained=%v work=%v current=%v reservable=%v proof=%+v err=%v", retained, validUnmirroredUnassembledWork(facts), validUnmirroredUnassembledCurrent(facts), splitTransferChildReservable(child), proof, err)
		}
		facts.NoParentNative = false
		if _, err = NewUnmirroredUnassembledSplitReserve(facts, child); err == nil {
			t.Fatal("missing native absence proof accepted")
		}
	}
}

func TestUnmirroredUnassembledRejectsUnprovenRetirementAndNativeHistory(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		alter func(*UnmirroredUnassembledSplitFacts)
	}{
		{"missing parent", func(f *UnmirroredUnassembledSplitFacts) { f.Parent = Posting{} }},
		{"ordinary cancellation", func(f *UnmirroredUnassembledSplitFacts) { f.Parent.Status = Present("cancelled") }},
		{"active full parent", func(f *UnmirroredUnassembledSplitFacts) { f.Parent.Status = Present("awaiting_packaging") }},
		{"unknown split", func(f *UnmirroredUnassembledSplitFacts) { f.Split.State = StateUnknown }},
		{"ACK incomplete", func(f *UnmirroredUnassembledSplitFacts) { f.Split.Receipt.UnassembledSplit = nil }},
		{"native owner", func(f *UnmirroredUnassembledSplitFacts) { f.NoParentNative = false }},
		{"frozen native owner", func(f *UnmirroredUnassembledSplitFacts) { f.OriginalCorrelation.CustomerOrderID = "known-native" }},
		{"changed product identity", func(f *UnmirroredUnassembledSplitFacts) {
			f.Before.Lines[0].ProductID = Present("known-original-product")
			f.Split.Receipt.ObservedPostings[0].Lines[0].ProductID = Present("different-catalog-product")
		}},
		{"missing child", func(f *UnmirroredUnassembledSplitFacts) { f.Children = f.Children[:1] }},
		{"foreign scope", func(f *UnmirroredUnassembledSplitFacts) { f.Parent.Scope.MoyskladAccount = "foreign" }},
		{"nested lineage", func(f *UnmirroredUnassembledSplitFacts) { f.Before.ParentPostingNumber = Present("ancestor") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts, child := unmirroredUnassembledFacts(t, false)
			tc.alter(&facts)
			if _, err := NewUnmirroredUnassembledSplitReserve(facts, child); err == nil {
				t.Fatal("unproven reserve permission accepted")
			}
		})
	}
}
