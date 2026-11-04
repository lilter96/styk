package orders

import (
	"encoding/json"
	"reflect"
	"testing"
)

func lineageFixture(t *testing.T) SplitLineageFacts {
	t.Helper()
	f, _ := supersededSplitFixture(t)
	return SplitLineageFacts{Scope: f.Before.Scope, RootPosting: f.Before.PostingNumber, SelectedPosting: f.Children[0].PostingNumber, Splits: []SplitLineageStep{{Work: f.Split, Before: f.Before}}, CurrentHeads: append([]Posting{f.Parent}, f.Children...)}
}

func lineageNestedFixture(t *testing.T) SplitLineageFacts {
	t.Helper()
	f := lineageFixture(t)
	before := cloneSplitLineagePostings(f.CurrentHeads[1:2])[0]
	first, second := cloneSplitLineagePostings([]Posting{before, before})[0], cloneSplitLineagePostings([]Posting{before})[0]
	first.PostingNumber, second.PostingNumber = "grandchild-one", "grandchild-two"
	first.ParentPostingNumber, second.ParentPostingNumber = Present(before.PostingNumber), Present(before.PostingNumber)
	first.Lines[0].Quantity, second.Lines[0].Quantity = Decimal("1"), Decimal("1")
	first.Evidence.Revision, second.Evidence.Revision = "grandchild-one-rev", "grandchild-two-rev"
	correlation, err := CorrelationFor(f.Scope, before.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	w := splitTransferWork(before, first, second, correlation, "nested-work")
	w.Command.ID, w.Receipt.CommandID = "nested-command", "nested-command"
	for i := range w.Command.PostingAction.Parameters.Packages {
		w.Command.PostingAction.Parameters.Packages[i].Lines[0].Quantity = "1"
	}
	f.Splits = append(f.Splits, SplitLineageStep{Work: w, Before: before})
	f.CurrentHeads[1].Status = Present(SupersededSplitParentStatus)
	f.CurrentHeads[1].Evidence.Revision = "nested-retired"
	f.CurrentHeads = append(f.CurrentHeads, first, second)
	f.SelectedPosting = first.PostingNumber
	return f
}

func TestSplitLineageNestedGenerationsAndUnorderedEvidence(t *testing.T) {
	t.Parallel()
	f := lineageNestedFixture(t)
	want := []string{f.Splits[0].Work.ID, f.Splits[1].Work.ID}
	f.Splits[0], f.Splits[1] = f.Splits[1], f.Splits[0]
	p, err := NewSplitLineage(f)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.PathWorkIDs(), want) || len(p.Frontier()) != 3 {
		t.Fatal("lost actual generation lineage")
	}
	if !p.CurrentMatches(f.CurrentHeads[3]) {
		t.Fatal("selected current observation mismatch")
	}
}

func TestSplitLineageUnassembledDoesNotInventStatus(t *testing.T) {
	t.Parallel()
	f := lineageFixture(t)
	f.Splits[0].Work.Command.Action = ActionPostingSplitUnassembled
	for i := range f.Splits[0].Work.Receipt.ObservedPostings {
		f.Splits[0].Work.Receipt.ObservedPostings[i].Status = Present("awaiting_packaging")
		f.CurrentHeads[i+1].Status = Present("awaiting_packaging")
	}
	if _, err := NewSplitLineage(f); err != nil {
		t.Fatal(err)
	}
}

func TestSplitLineageWitnessFrozenAndNotSerializableAuthority(t *testing.T) {
	t.Parallel()
	f := lineageFixture(t)
	p, err := NewSplitLineage(f)
	if err != nil {
		t.Fatal(err)
	}
	selected := cloneSplitLineagePostings(f.CurrentHeads[1:2])[0]
	f.CurrentHeads[1].Lines[0].Quantity = Decimal("999")
	frontier := p.Frontier()
	frontier[0].Lines[0].Quantity = Decimal("999")
	if p.Validate() != nil || !p.CurrentMatches(selected) {
		t.Fatal("caller mutated frozen proof")
	}
	var forged SplitLineageProof
	if err := lineageJSONRoundTrip(p, &forged); err != nil {
		t.Fatal(err)
	}
	if forged.Validate() == nil || (SplitLineageProof{}).Validate() == nil {
		t.Fatal("serialized or literal proof gained authority")
	}
}

func lineageJSONRoundTrip(value, target any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
