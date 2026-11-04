package orders

import (
	"slices"
	"testing"
)

type lineageMutation struct {
	name  string
	alter func(*SplitLineageFacts)
}

func TestSplitLineageRejectsIncompleteOrConflictingHistory(t *testing.T) {
	t.Parallel()
	for _, tc := range lineageBadFacts() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := lineageNestedFixture(t)
			tc.alter(&f)
			if _, err := NewSplitLineage(f); err == nil {
				t.Fatal("unproven lineage accepted")
			}
		})
	}
}

func lineageBadFacts() []lineageMutation {
	return []lineageMutation{
		{"unknown ancestor", func(f *SplitLineageFacts) { f.Splits[0].Work.State = StateUnknown }},
		{"ack without readback", func(f *SplitLineageFacts) { f.Splits[1].Work.Receipt.State = StateAcknowledged }},
		{"foreign work", func(f *SplitLineageFacts) { f.Splits[1].Work.Scope.OzonAccount = "foreign" }},
		{"foreign correlation", func(f *SplitLineageFacts) { f.Splits[1].Work.Command.Correlation.Scope.OzonAccount = "foreign" }},
		{"nonconserving split", func(f *SplitLineageFacts) {
			f.Splits[1].Work.Command.PostingAction.Parameters.Packages[0].Lines[0].Quantity = "2"
		}},
		{"missing ancestor", func(f *SplitLineageFacts) { f.Splits = f.Splits[1:] }},
		{"missing head", func(f *SplitLineageFacts) { f.CurrentHeads = f.CurrentHeads[1:] }},
		{"duplicate head", func(f *SplitLineageFacts) { f.CurrentHeads = append(f.CurrentHeads, f.CurrentHeads[0]) }},
		{"future unknown status", func(f *SplitLineageFacts) { f.CurrentHeads[1].Status = Field[string]{} }},
		{"ordinary canceled ancestor", func(f *SplitLineageFacts) { f.CurrentHeads[1].Status = Present("cancelled") }},
		{"failed child", func(f *SplitLineageFacts) { f.CurrentHeads[3].Substatus = Present("ship_failed") }},
		{"changed current allocation", func(f *SplitLineageFacts) { f.CurrentHeads[3].Lines[0].Quantity = "2" }},
		{"foreign native warehouse", func(f *SplitLineageFacts) { f.CurrentHeads[3].WarehouseID = Present("other") }},
		{"external provider parent", func(f *SplitLineageFacts) { f.CurrentHeads[3].ParentPostingNumber = Present("stranger") }},
		{"duplicate producer", func(f *SplitLineageFacts) { f.Splits = append(f.Splits, f.Splits[1]) }},
		{"malformed warehouse", func(f *SplitLineageFacts) { f.CurrentHeads[3].WarehouseID = Field[string]{Presence: PresenceValue} }},
		{"head cap", func(f *SplitLineageFacts) { f.CurrentHeads = make([]Posting, MaxSplitLineageHeads+1) }},
		{"step cap", func(f *SplitLineageFacts) { f.Splits = make([]SplitLineageStep, MaxSplitLineageSteps+1) }},
	}
}

func TestSplitLineageRetainedSameNumberIsGenerationNotCycle(t *testing.T) {
	t.Parallel()
	base := splitTransferFixture(t)
	first := SplitLineageStep{Work: base.Split, Before: base.Before}
	before := cloneSplitLineagePostings([]Posting{base.Parent})[0]
	retained, child := cloneSplitLineagePostings([]Posting{before})[0], cloneSplitLineagePostings([]Posting{before})[0]
	retained.Lines[0].Quantity, child.Lines[0].Quantity = "1", "1"
	retained.Evidence.Revision, child.Evidence.Revision = "retained-next", "child-next"
	child.PostingNumber = "second-child"
	child.ParentPostingNumber = Present(before.PostingNumber)
	second := splitTransferWork(before, retained, child, base.OriginalCorrelation, "second-generation")
	second.Command.ID, second.Receipt.CommandID = "second-command", "second-command"
	for i := range second.Command.PostingAction.Parameters.Packages {
		second.Command.PostingAction.Parameters.Packages[i].Lines[0].Quantity = "1"
	}
	facts := SplitLineageFacts{Scope: before.Scope, RootPosting: before.PostingNumber, SelectedPosting: before.PostingNumber, Splits: []SplitLineageStep{first, {Work: second, Before: before}}, CurrentHeads: []Posting{retained, base.Child, child}}
	proof, err := NewSplitLineage(facts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(proof.PathWorkIDs(), []string{base.Split.ID, second.ID}) || len(proof.Frontier()) != 3 {
		t.Fatal("retained generation collapsed")
	}
}
