package orders

import (
	"fmt"
	"strconv"
	"testing"
)

func lineageRetainedChain(t *testing.T, depth int) SplitLineageFacts {
	t.Helper()
	f := lineageFixture(t)
	before := cloneSplitLineagePostings([]Posting{f.Splits[0].Before})[0]
	f.Splits = nil
	f.CurrentHeads = nil
	quantity := int64(1) << (MaxSplitLineageDepth + 1)
	before.Lines[0].Quantity = Decimal(strconv.FormatInt(quantity, 10))
	f.SelectedPosting = before.PostingNumber
	for i := 0; i < depth; i++ {
		quantity /= 2
		retained := cloneSplitLineagePostings([]Posting{before})[0]
		retained.Lines[0].Quantity = Decimal(strconv.FormatInt(quantity, 10))
		retained.Evidence.Revision = fmt.Sprintf("generation-%d", i)
		child := cloneSplitLineagePostings([]Posting{retained})[0]
		child.PostingNumber = fmt.Sprintf("child-%d", i)
		child.ParentPostingNumber = Present(before.PostingNumber)
		correlation, err := CorrelationFor(f.Scope, before.PostingNumber)
		if err != nil {
			t.Fatal(err)
		}
		work := splitTransferWork(before, retained, child, correlation, fmt.Sprintf("work-%d", i))
		work.Command.ID = fmt.Sprintf("command-%d", i)
		work.Receipt.CommandID = work.Command.ID
		for j := range work.Command.PostingAction.Parameters.Packages {
			work.Command.PostingAction.Parameters.Packages[j].Lines[0].Quantity = retained.Lines[0].Quantity
		}
		f.Splits = append(f.Splits, SplitLineageStep{Work: work, Before: before})
		f.CurrentHeads = append(f.CurrentHeads, child)
		before = retained
	}
	f.CurrentHeads = append(f.CurrentHeads, before)
	return f
}

func TestSplitLineageLocalDepthBound(t *testing.T) {
	t.Parallel()
	valid := lineageRetainedChain(t, MaxSplitLineageDepth)
	if _, err := NewSplitLineage(valid); err != nil {
		t.Fatal("bounded generation chain rejected", err)
	}
	invalid := lineageRetainedChain(t, MaxSplitLineageDepth+1)
	if _, err := NewSplitLineage(invalid); err == nil {
		t.Fatal("unbounded structural traversal accepted")
	}
}
