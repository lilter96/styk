//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersPartialCancellationRejectsPreAttemptCompletionAtomically(t *testing.T) {
	f, _ := partialMirrorFixture(t)
	original := f.preview(t).Decisions[0].Posting
	proposal := orders.ActionProposal{PostingNumber: original.PostingNumber, Action: orders.ActionPostingPartialCancel, Parameters: orders.ActionParameters{ReasonID: "352", ReasonMessage: "SYNTHETIC-PRIVATE-REASON", Lines: []orders.ActionQuantity{{Identity: original.Lines[0].Identity, Quantity: "1"}}}}
	preview := partialMirrorPreview(t, f, []orders.ActionProposal{proposal})
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	work := ordersStoreClaim(t, f.ordersStoreFixture, time.Minute)
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := binding.Client.ReadOrderPosting(f.ctx, f.config.Scope, original.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	stale := actual
	stale.Evidence.CheckedAt = work.Receipt.ObservedAt.Add(-time.Second)
	stale.Evidence.ObservedAt = stale.Evidence.CheckedAt
	completion := orders.Completion{WorkID: work.ID, Scope: work.Scope, ClaimToken: work.ClaimToken, ExpectedState: work.State, State: orders.StateReconciled, ObservedPosting: &stale}
	for _, checked := range []time.Time{stale.Evidence.CheckedAt, {}} {
		stale.Evidence.CheckedAt = checked
		if err = f.db.CompleteOrderWork(f.ctx, completion); !errors.Is(err, orders.ErrStale) {
			t.Fatal("pre-attempt cancellation completed", err)
		}
	}
	heads, err := f.db.OrderPostings(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10, Search: original.PostingNumber})
	if err != nil || len(heads.Postings) != 1 || heads.Postings[0].Lines[0].Quantity != "2" {
		t.Fatal("rejected completion changed admitted source facts", err)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_reconciliations`, 1)
	completion.ObservedPosting = &actual
	if err = f.db.CompleteOrderWork(f.ctx, completion); err != nil {
		t.Fatal("fresh exact-target readback rejected", err)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_reconciliations`, 2)
}
