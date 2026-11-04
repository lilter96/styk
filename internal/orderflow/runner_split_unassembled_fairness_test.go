package orderflow

import (
	"context"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestUnassembledSplitRefreshesObservedPendingOriginal(t *testing.T) {
	t.Parallel()
	store, reader, runner := normalizedSplitFixture(t)
	root := store.work.Command.PostingNumber
	parent := reader.children[root]
	parent.Status = orders.Present("awaiting_packaging")
	reader.children[root] = parent
	for number, posting := range reader.children {
		copy := posting
		store.reads = append(store.reads, orders.SplitReadback{PostingNumber: number, Posting: &copy, Code: "OBSERVED", CheckedAt: flowTestNow})
	}
	writes := reader.writes
	if err := runner.readbackUnassembledSplit(context.Background(), *store.work, reader); err != nil {
		t.Fatal(err)
	}
	if store.work.State != orders.StatePolling {
		t.Fatal("old full parent falsely reconciled")
	}
	parent.Status = orders.Present(orders.SupersededSplitParentStatus)
	reader.children[root] = parent
	for range 4 {
		if store.work.State == orders.StateReconciled {
			break
		}
		if err := runner.readbackUnassembledSplit(context.Background(), *store.work, reader); err != nil {
			t.Fatal(err)
		}
	}
	if store.work.State != orders.StateReconciled || reader.reads[root] == 0 {
		t.Fatal("successful but stale cached original was never refreshed")
	}
	if reader.writes != writes {
		t.Fatal("pending split replayed provider write")
	}
}

func TestUnassembledSplitFailedFirstPageCannotStarveUnreadOriginal(t *testing.T) {
	t.Parallel()
	store, reader, runner := normalizedSplitFixture(t)
	candidates, err := orders.UnassembledSplitCandidates(store.work.Command, store.work.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, number := range candidates[:10] {
		delete(reader.children, number)
		store.reads = append(store.reads, orders.SplitReadback{PostingNumber: number, Code: "SOURCE_UNAVAILABLE", CheckedAt: flowTestNow})
	}
	for range 3 {
		if err = runner.readbackUnassembledSplit(context.Background(), *store.work, reader); err != nil {
			t.Fatal(err)
		}
	}
	if reader.reads[candidates[10]] == 0 || reader.reads[store.work.Command.PostingNumber] == 0 {
		t.Fatal("failed first ten candidates starved never-attempted original")
	}
	if store.work.State != orders.StatePolling {
		t.Fatal("missing allocations falsely reconciled")
	}
}
