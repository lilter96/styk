package orderflow

import (
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestUnassembledSplitInvalidDetailCannotDiscardPageOrStarveLaterReads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		alter func(*orders.Posting, orders.Work)
	}{
		{"foreign tenant", func(p *orders.Posting, _ orders.Work) { p.Scope.Tenant = "foreign" }},
		{"foreign account", func(p *orders.Posting, _ orders.Work) { p.Scope.OzonAccount = "foreign" }},
		{"wrong posting", func(p *orders.Posting, _ orders.Work) { p.PostingNumber = "foreign-posting" }},
		{"wrong commercial order", func(p *orders.Posting, _ orders.Work) { p.CommercialOrderID = "foreign-order" }},
		{"invalid identity", func(p *orders.Posting, _ orders.Work) { p.PostingNumber = "" }},
		{"wrong evidence origin", func(p *orders.Posting, _ orders.Work) { p.Evidence.Origin = orders.OriginUser }},
		{"before dispatch", func(p *orders.Posting, w orders.Work) { p.Evidence.CheckedAt = w.Receipt.ObservedAt.Add(-time.Second) }},
		{"future check", func(p *orders.Posting, _ orders.Work) { p.Evidence.CheckedAt = flowTestNow.Add(time.Minute) }},
		{"future observation", func(p *orders.Posting, _ orders.Work) { p.Evidence.ObservedAt = flowTestNow.Add(time.Minute) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertInvalidSplitPointProgress(t, tc.alter)
		})
	}
}

func assertInvalidSplitPointProgress(t *testing.T, alter func(*orders.Posting, orders.Work)) {
	t.Helper()
	store, reader, runner := normalizedSplitFixture(t)
	candidates, err := orders.UnassembledSplitCandidates(store.work.Command, store.work.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	number := candidates[0]
	original := reader.children[number]
	invalid := original
	alter(&invalid, *store.work)
	reader.children[number] = invalid
	if err = runner.readbackUnassembledSplit(context.Background(), *store.work, reader); err != nil {
		t.Fatal(err)
	}
	if len(store.reads) != 10 || store.work.State != orders.StatePolling {
		t.Fatalf("invalid point discarded page: state=%s reads=%d", store.work.State, len(store.reads))
	}
	for _, read := range store.reads {
		if read.PostingNumber == number && (read.Code != "SOURCE_UNAVAILABLE" || read.Posting != nil) {
			t.Fatal("invalid provider body admitted")
		}
	}
	if err = runner.readbackUnassembledSplit(context.Background(), *store.work, reader); err != nil {
		t.Fatal(err)
	}
	for _, later := range candidates[10:] {
		if reader.reads[later] != 1 {
			t.Fatal("invalid first page starved later candidate")
		}
	}
	if store.work.State == orders.StateReconciled {
		t.Fatal("invalid detail falsely reconciled")
	}
	reader.children[number] = original
	assertSplitPointRecovery(t, store, reader, runner)
}

func assertSplitPointRecovery(t *testing.T, store *normalizedSplitStore, reader *splitReaderFixture, runner *Runner) {
	t.Helper()

	for range 4 {
		if store.work.State == orders.StateReconciled {
			break
		}
		if err := runner.readbackUnassembledSplit(context.Background(), *store.work, reader); err != nil {
			t.Fatal(err)
		}
	}
	if store.work.State != orders.StateReconciled {
		t.Fatal("corrected detail never recovered")
	}
}
