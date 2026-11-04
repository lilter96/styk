//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersSplitReconciliationPreservesChildEvidence(t *testing.T) {
	f := newOrdersStoreFixture(t)
	before := ordersStorePosting(f.config.Scope, "split-parent", "source", time.Now().UTC())
	ordersStoreCommit(t, f, before, "split-source")
	preview := splitStorePreview(t, f, before)
	ordersStoreApprove(t, f, preview)
	w := ordersStoreClaim(t, f, time.Minute)
	allowed, err := f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: w, Begin: true, Now: time.Now()})
	if err != nil || !allowed {
		t.Fatalf("dispatch: %v", err)
	}
	receipt := orders.Receipt{CommandID: w.Command.ID, State: orders.StateAcknowledged, PostingNumbers: []string{before.PostingNumber, "split-child"}, HTTPStatus: 200, ObservedAt: time.Now().UTC()}
	ack := orders.Completion{WorkID: w.ID, Scope: w.Scope, ClaimToken: w.ClaimToken, ExpectedState: orders.StateDispatching, State: orders.StateAcknowledged, Receipt: receipt}
	if err = f.db.CompleteOrderWork(f.ctx, ack); err != nil {
		t.Fatal(err)
	}
	w = ordersStoreClaim(t, f, time.Minute)
	children := []orders.Posting{before, before}
	for i, number := range receipt.PostingNumbers {
		children[i].PostingNumber = number
		children[i].Lines = append([]orders.PostingLine(nil), before.Lines...)
		children[i].Lines[0].Quantity = "1"
		children[i].Status = orders.Present("awaiting_deliver")
		children[i].Evidence.ObservedAt = time.Now().UTC()
		children[i].Evidence.CheckedAt = children[i].Evidence.ObservedAt
	}
	receipt.State, receipt.ObservedPostings = orders.StateReconciled, children
	done := orders.Completion{WorkID: w.ID, Scope: w.Scope, ClaimToken: w.ClaimToken, ExpectedState: w.State, State: orders.StateReconciled, Receipt: receipt}
	children[0].Lines[0].Quantity = "2"
	if err = f.db.CompleteOrderWork(f.ctx, done); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("incorrect allocation reconciled: %v", err)
	}
	children[0].Lines[0].Quantity = "1"
	if err = f.db.CompleteOrderWork(f.ctx, done); err != nil {
		t.Fatal(err)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_receipts WHERE jsonb_array_length(receipt->'observed_postings')=2`, 1)
	if err = f.db.CompleteOrderWork(f.ctx, done); err == nil {
		t.Fatal("terminal claim reused")
	}
}

func splitStorePreview(t *testing.T, f ordersStoreFixture, before orders.Posting) orders.Preview {
	t.Helper()
	p := ordersStorePreview(t, f, before.PostingNumber)
	p.ID += "-split"
	c := &p.Decisions[0].Commands[0]
	c.ID += "-split"
	c.IdempotencyKey = c.ID
	c.Action = orders.ActionPostingSplit
	c.CustomerOrder = nil
	cap := orders.CapabilityRef{ID: "synthetic-capability", Revision: 1, WarehouseID: *before.WarehouseID.Value, DeliveryMethodID: *before.DeliveryMethodID.Value, Scheme: "FBS", ObservedAt: time.Now().UTC(), ValidUntil: time.Now().UTC().Add(time.Hour)}
	c.Warehouse.Capability = cap
	line := orders.ActionQuantity{Identity: before.Lines[0].Identity, Quantity: "1"}
	c.PostingAction = &orders.PostingAction{Capability: cap, Parameters: orders.ActionParameters{Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{line}}, {Lines: []orders.ActionQuantity{line}}}, ReadinessConfirmed: true}}
	p, err := f.db.SaveOrderPreview(f.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
