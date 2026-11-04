//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func splitReserveFixture(t *testing.T) (ordersStoreFixture, orders.Posting, orders.Work) {
	t.Helper()
	f := newOrdersStoreFixture(t)
	config := f.config
	config.FieldMappings = append(config.FieldMappings, orders.FieldMapping{Source: orders.ReserveOpenQuantitySource, Target: "positions.reserve"}, orders.FieldMapping{Source: "constant.true", Target: "applicable"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "split-reserve-guard"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before := ordersStorePosting(f.config.Scope, "split-parent", "before", time.Now().UTC())
	ordersStoreCommit(t, f, before, "split-source")
	sibling := ordersStorePosting(f.config.Scope, "existing-sibling", "sibling", time.Now().UTC())
	ordersStoreCommit(t, f, sibling, "sibling-source")
	preview := splitStorePreview(t, f, before)
	ordersStoreApprove(t, f, preview)
	queuedSibling := ordersStorePosting(f.config.Scope, "queued-sibling", "queued-sibling-source", time.Now().UTC())
	ordersStoreCommit(t, f, queuedSibling, "queued-sibling-admission")
	work := ordersStoreClaim(t, f, time.Minute)
	allowed, err := f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: work, Begin: true, Now: time.Now()})
	if err != nil || !allowed {
		t.Fatal("split dispatch guard", err)
	}
	return f, before, work
}

func TestOrdersSplitReserveGuardDoesNotInventProviderParentOrBlockExistingSibling(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "known_children", true: "unknown_result"}[unknown], func(t *testing.T) {
			f, before, work := splitReserveFixture(t)
			state := orders.StateAcknowledged
			numbers := []string{before.PostingNumber, "split-child"}
			if unknown {
				state = orders.StateUnknown
				numbers = nil
			}
			receipt := orders.Receipt{CommandID: work.Command.ID, State: state, PostingNumbers: numbers, HTTPStatus: 200, ObservedAt: time.Now().UTC()}
			if err := f.db.CompleteOrderWork(f.ctx, orders.Completion{WorkID: work.ID, Scope: work.Scope, ClaimToken: work.ClaimToken, ExpectedState: orders.StateDispatching, State: state, Receipt: receipt}); err != nil {
				t.Fatal(err)
			}
			child := ordersStorePosting(f.config.Scope, "split-child", "actual-child", time.Now().UTC())
			child.Lines[0].Quantity = "1"
			if child.ParentPostingNumber.IsKnown() {
				t.Fatal("fixture must not supply provider lineage")
			}
			ordersStoreCommit(t, f, child, "new-child-source")
			review, err := f.db.OrderReviewContextFor(f.ctx, f.config.Scope, []string{before.PostingNumber, child.PostingNumber, "existing-sibling", "queued-sibling"}, false)
			if err != nil {
				t.Fatal(err)
			}
			if !review.SplitReserveBlocked[child.PostingNumber] || review.SplitReserveBlocked[before.PostingNumber] || review.SplitReserveBlocked["existing-sibling"] || review.SplitReserveBlocked["queued-sibling"] {
				t.Fatalf("split dependency crossed independent posting: %+v", review.SplitReserveBlocked)
			}
			observed, err := f.db.OrderPostings(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10, Search: child.PostingNumber})
			if err != nil || len(observed.Postings) != 1 || observed.Postings[0].ParentPostingNumber.IsKnown() {
				t.Fatal("local reserve dependency was forged into provider lineage", err)
			}
		})
	}
}
