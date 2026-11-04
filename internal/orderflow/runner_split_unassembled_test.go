package orderflow

import (
	"context"
	"github.com/lilter96/styk/internal/orders"
	"testing"
)

type normalizedSplitStore struct {
	*flowStore
	reads []orders.SplitReadback
}

func (s *normalizedSplitStore) OrderSplitReadbacks(context.Context, orders.Scope, string) ([]orders.SplitReadback, error) {
	return s.reads, nil
}
func (s *normalizedSplitStore) CompleteOrderWork(ctx context.Context, completion orders.Completion) error {
	if err := s.flowStore.CompleteOrderWork(ctx, completion); err != nil {
		return err
	}
	s.reads = mergeUnassembledReads(s.reads, completion.SplitReads)
	return nil
}

func TestUnassembledSplitResumesNormalizedProgressWithoutReplayingWrite(t *testing.T) {
	t.Parallel()
	store, source, target, runner := dispatchedFlow(t)
	reader := prepareSplitReadback(t, store, source)
	w := store.work
	w.Command.Action = orders.ActionPostingSplitUnassembled
	preview := store.previews[w.PreviewID]
	before := preview.Decisions[0].Posting
	before.Lines[0].Identity.SKU = "9007199254740993"
	preview.Decisions[0].Posting = before
	for i := range w.Command.PostingAction.Parameters.Packages {
		w.Command.PostingAction.Parameters.Packages[i].Lines[0].Identity.SKU = before.Lines[0].Identity.SKU
	}
	outputs := make([]orders.SplitResponsePosting, 0, len(reader.children))
	for number, child := range reader.children {
		child.Lines[0].Identity.SKU = before.Lines[0].Identity.SKU
		reader.children[number] = child
		outputs = append(outputs, orders.SplitResponsePosting{PostingNumber: orders.Present(number), Products: orders.Present([]orders.SplitResponseProduct{{SKU: before.Lines[0].Identity.SKU, Quantity: "1"}})})
	}
	before.Status = orders.Present(orders.SupersededSplitParentStatus)
	reader.children[before.PostingNumber] = before
	var err error
	w.CommandFingerprint, err = orders.CommandFingerprint(w.Command)
	if err != nil {
		t.Fatal(err)
	}
	preview.Decisions[0].Commands = []orders.Command{w.Command}
	store.previews[w.PreviewID] = preview
	w.Receipt.HTTPStatus = 200
	w.Receipt.PostingNumbers = nil
	w.Receipt.UnassembledSplit = &orders.UnassembledSplitAck{Postings: orders.Present(outputs)}
	normalized := &normalizedSplitStore{flowStore: store}
	runner.store = normalized
	if err := runner.readbackUnassembledSplit(context.Background(), *w, reader); err != nil {
		t.Fatal(err)
	}
	if w.State != orders.StatePolling || len(normalized.reads) != 10 || w.Receipt.ObservedPostings != nil || len(w.Receipt.PostingNumbers) != 0 {
		t.Fatalf("first page state=%s code=%s reads=%d", w.State, w.Code, len(normalized.reads))
	}
	runner.store = normalized // A restarted runner relies on persisted rows, not an in-process cursor.
	if err := runner.readbackUnassembledSplit(context.Background(), *w, reader); err != nil {
		t.Fatal(err)
	}
	if w.State != orders.StateReconciled || len(w.Receipt.PostingNumbers) != 11 || len(normalized.reads) != 12 || w.Receipt.ObservedPostings != nil {
		t.Fatal("complete normalized allocation did not reconcile")
	}
	for number, count := range reader.reads {
		if count != 1 {
			t.Fatalf("candidate %s read %d times", number, count)
		}
	}
	if target.writes != 0 {
		t.Fatal("readback replayed mutation")
	}
}
