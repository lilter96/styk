package orderflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type splitReaderFixture struct {
	*flowProvider
	children map[string]orders.Posting
	reads    map[string]int
}

func (p *splitReaderFixture) ReadOrderPosting(_ context.Context, _ orders.Scope, number string) (orders.Posting, error) {
	p.reads[number]++
	posting, ok := p.children[number]
	if !ok {
		return orders.Posting{}, orders.ErrNotFound
	}
	return posting, nil
}

func TestSplitReadbackResumesBoundedProgressAndRefreshesPendingChild(t *testing.T) {
	t.Parallel()
	store, source, target, runner := dispatchedFlow(t)
	reader := prepareSplitReadback(t, store, source)
	if err := runner.readbackSplit(context.Background(), *store.work, reader); err != nil {
		t.Fatal(err)
	}
	if store.work.State != orders.StatePolling || len(store.work.Receipt.ObservedPostings) != 10 {
		t.Fatal("bounded progress not persisted")
	}
	restarted, err := NewRunner(store, flowResolver{source: source, target: target}, RunnerOptions{WorkerID: "replacement", Now: func() time.Time { return flowTestNow }})
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.readbackSplit(context.Background(), *store.work, reader); err != nil {
		t.Fatal(err)
	}
	if len(store.work.Receipt.ObservedPostings) != 10 {
		t.Fatal("pending child cached as completed")
	}
	last := reader.children["child-10"]
	last.Status = orders.Present("awaiting_deliver")
	reader.children["child-10"] = last
	if err = restarted.readbackSplit(context.Background(), *store.work, reader); err != nil {
		t.Fatal(err)
	}
	if store.work.State != orders.StateReconciled || len(store.work.Receipt.ObservedPostings) != 11 {
		t.Fatal("complete split not reconciled")
	}
	if reader.reads["child-0"] != 1 || reader.reads["child-10"] != 2 || target.writes != 0 {
		t.Fatal("readback duplicated reads or wrote externally")
	}
}

func prepareSplitReadback(t *testing.T, store *flowStore, source *flowProvider) *splitReaderFixture {
	t.Helper()
	w := store.work
	preview := store.previews[w.PreviewID]
	before := source.posting
	before.Lines = append([]orders.PostingLine(nil), before.Lines...)
	before.Lines[0].Quantity = "11"
	command := w.Command
	command.Action = orders.ActionPostingSplit
	command.CustomerOrder = nil
	cap := orders.CapabilityRef{ID: "cap", Revision: 1, WarehouseID: "warehouse", DeliveryMethodID: "delivery-method", Scheme: "FBS", ObservedAt: flowTestNow, ValidUntil: flowTestNow.Add(time.Hour)}
	command.Warehouse.Capability = cap
	command.PostingAction = &orders.PostingAction{Capability: cap}
	reader := &splitReaderFixture{flowProvider: source, children: make(map[string]orders.Posting), reads: make(map[string]int)}
	for i := range 11 {
		number := fmt.Sprintf("child-%d", i)
		command.PostingAction.Parameters.Packages = append(command.PostingAction.Parameters.Packages, orders.ActionPackage{Lines: []orders.ActionQuantity{{Identity: before.Lines[0].Identity, Quantity: "1"}}})
		child := before
		child.PostingNumber = number
		child.Lines = append([]orders.PostingLine(nil), before.Lines...)
		child.Lines[0].Quantity = "1"
		child.Status = orders.Present("awaiting_deliver")
		if i == 10 {
			child.Status = orders.Present("awaiting_packaging")
		}
		reader.children[number] = child
		w.Receipt.PostingNumbers = append(w.Receipt.PostingNumbers, number)
	}
	preview.Decisions[0].Posting = before
	preview.Decisions[0].Commands = []orders.Command{command}
	store.previews[w.PreviewID] = preview
	w.Command = command
	var err error
	w.CommandFingerprint, err = orders.CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	w.State = orders.StateAcknowledged
	w.Receipt.CommandID = command.ID
	w.Receipt.State = w.State
	w.Receipt.ObservedAt = flowTestNow.Add(-time.Second)
	return reader
}
