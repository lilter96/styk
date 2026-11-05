//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersNestedSettlementMirrorsOnlyThreeConservedTerminalAllocations(t *testing.T) {
	f, p, root := nestedSettlementHTTPFixture(t, false)
	nestedSettlementSplit(t, f, root.PostingNumber, "2")
	nestedSettlementSplit(t, f, nestedChild, "1")
	for _, target := range []struct {
		number   string
		quantity orders.Decimal
	}{{nestedGrandA, "1"}, {nestedGrandB, "1"}, {nestedSibling, "2"}} {
		preview := splitMemberHTTPPreview(t, f, target.number, nil)
		command := preview.Decisions[0].Commands[0]
		if command.Action != orders.ActionMirrorCreate || command.CustomerOrder.Positions[0].Quantity != target.quantity || !command.CustomerOrder.Positions[0].Reserve.IsKnown() || *command.CustomerOrder.Positions[0].Reserve.Value != target.quantity {
			t.Fatal("nested allocation was not reserved exactly")
		}
		f.approve(t, preview)
		f.run(t, orders.StateAcknowledged)
		f.run(t, orders.StateReconciled)
		actual := f.readNative(t)
		if len(actual.Positions) != 1 || actual.Positions[0].Quantity != target.quantity || !actual.Positions[0].Reserve.IsKnown() || *actual.Positions[0].Reserve.Value != target.quantity {
			t.Fatal("native readback lost conserved terminal allocation")
		}
		if noop := splitMemberHTTPPreview(t, f, target.number, nil); len(noop.Decisions[0].Commands) != 0 {
			t.Fatal("unchanged terminal allocation duplicated native order")
		}
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action='mirror_create' AND dispatch_count=1 AND state='RECONCILED'`, 3)
	if p.writes.Load() != 2 {
		t.Fatal("nested split request was repeated")
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if len(f.provider.base.native) != 3 {
		t.Fatal("retired whole parent or duplicate native document created")
	}
}

func TestOrdersNestedSettlementCannotForgetPreviouslyReservedFullAncestor(t *testing.T) {
	f, p, root := nestedSettlementHTTPFixture(t, false)
	f.approve(t, f.preview(t))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	nestedSettlementSplit(t, f, root.PostingNumber, "2")
	nestedSettlementSplit(t, f, nestedChild, "1")
	blocked := unmirroredUnassembledHTTPReview(t, f, nestedGrandA)
	if blocked.Decisions[0].Ready || len(blocked.Decisions[0].Commands) != 0 {
		t.Fatal("nested proof hid original full native reserve")
	}
	if p.writes.Load() != 2 {
		t.Fatal("blocked nested reserve replayed split")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action='mirror_create' AND dispatch_count=1`, 1)
}

func nestedSettlementSplit(t *testing.T, f positionHTTPFixture, number string, quantity orders.Decimal) {
	t.Helper()
	review := unmirroredUnassembledHTTPReview(t, f, number)
	if len(review.Decisions) != 1 {
		t.Fatal("missing nested source")
	}
	posting := review.Decisions[0].Posting
	line := orders.ActionQuantity{Identity: posting.Lines[0].Identity, Quantity: quantity}
	proposal := orders.ActionProposal{PostingNumber: number, Action: orders.ActionPostingSplitUnassembled, Parameters: orders.ActionParameters{Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{line}}, {Lines: []orders.ActionQuantity{line}}}}}
	f.approve(t, splitMemberHTTPPreview(t, f, number, []orders.ActionProposal{proposal}))
	nestedSettlementAdvance(t, f, orders.StateAcknowledged)
	nestedSettlementAdvance(t, f, orders.StateReconciled)
}

func TestOrdersNestedSettlementUnknownAncestorAdmissionStillFencesGrandchildren(t *testing.T) {
	f, p, root := nestedSettlementHTTPFixture(t, true)
	nestedSettlementSplit(t, f, root.PostingNumber, "2")
	parent := splitMemberHTTPPreview(t, f, root.PostingNumber, nil)
	f.provider.base.mu.Lock()
	f.provider.base.lost[root.PostingNumber] = true
	f.provider.base.mu.Unlock()
	f.approve(t, parent)
	f.run(t, orders.StateUnknown)
	unknown := f.latestWork(t)
	nestedSettlementSplit(t, f, nestedChild, "1")
	blocked := unmirroredUnassembledHTTPReview(t, f, nestedGrandA)
	if blocked.Decisions[0].Ready || len(blocked.Decisions[0].Commands) != 0 {
		t.Fatal("UNKNOWN ancestor admission was hidden by nested split")
	}
	history, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, operation := range history.Operations {
		for _, work := range operation.Targets {
			if work.ID == unknown.ID {
				found = work.State == orders.StateUnknown && work.DispatchCount == 1
			}
		}
	}
	if !found || p.writes.Load() != 2 {
		t.Fatal("original UNKNOWN was rewritten or split replayed")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE action='mirror_create' AND dispatch_count=1`, 1)
}

func nestedSettlementAdvance(t *testing.T, f positionHTTPFixture, state orders.State) {
	t.Helper()
	for range 4 {
		time.Sleep(2 * time.Millisecond)
		ran, err := f.runner.RunNext(f.ctx)
		if err != nil || !ran {
			t.Fatalf("nested normalworker phase ran=%v err=%v", ran, err)
		}
		if f.latestWork(t).State == state {
			return
		}
	}
	t.Fatalf("nested operation did not reach %s", state)
}
