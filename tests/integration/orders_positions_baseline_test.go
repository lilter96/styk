//go:build integration

package integration

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersPositionPatchRequiresImmutableOwnedBaseline(t *testing.T) {
	f, p, target := positionBaselineFixture(t)
	command := &p.Decisions[0].Commands[0]
	original := command.PositionPatch.Baseline
	command.PositionPatch.Baseline.WorkID = "forged-previous-work"
	if _, err := f.db.SaveOrderPreview(f.ctx, p); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("forged native-line ownership admitted: %v", err)
	}
	command.PositionPatch.Baseline = original
	saved, err := f.db.SaveOrderPreview(f.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	ordersStoreApprove(t, f, saved)
	work := ordersStoreClaim(t, f, time.Minute)
	if allowed, err := f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: work, Begin: true}); err != nil || !allowed {
		t.Fatalf("exact position dispatch blocked: %v %v", allowed, err)
	}
	if allowed, err := f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: work, Begin: true}); err != nil || allowed {
		t.Fatalf("position intent sent twice: %v %v", allowed, err)
	}
	work = ackPositionPatch(t, f, work)
	target.Positions[0].Quantity = "1"
	target.Positions[1].Reserve = orders.Present(orders.Decimal("1"))
	done := scalarReadbackCompletion(work, target)
	if err = f.db.CompleteOrderWork(f.ctx, done); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("foreign reserve mutation reconciled: %v", err)
	}
	target.Positions[1].Reserve = orders.Present(orders.Decimal("2"))
	if err = f.db.CompleteOrderWork(f.ctx, done); err != nil {
		t.Fatal(err)
	}
	assertPositionBaselineAdvanced(t, f, work)
}

func positionBaselineFixture(t *testing.T) (ordersStoreFixture, orders.Preview, orders.TargetObservation) {
	t.Helper()
	f, posting, target := initialPositionBaseline(t)
	posting.Lines[0].Quantity = "1"
	posting.Evidence.Revision = "source-new"
	posting.Evidence.ProviderUpdatedAt = orders.Present(time.Now().UTC())
	ordersStoreCommit(t, f, posting, "position-new")
	current, err := f.db.OrderReviewContext(f.ctx, f.config.Scope)
	if err != nil {
		t.Fatal(err)
	}
	target.Positions = append(target.Positions, orders.TargetPosition{ID: "foreign-position", MoyskladKind: "product", MoyskladID: "foreign-product", Quantity: "7", Reserve: orders.Present(orders.Decimal("2"))})
	now := time.Now().UTC()
	p := orders.Preview{ID: "position-preview", Scope: f.config.Scope, ConfigRevision: f.config.Revision, Credentials: current.Credentials, Actor: f.session.AuditID, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	decision := ordersStoreDecision(t, current, posting.PostingNumber, p.ID, now)
	c := &decision.Commands[0]
	c.Action = orders.ActionMirrorUpdate
	c.ExpectedTargetRevision = target.Revision
	c.TargetPositions = slices.Clone(target.Positions)
	c.TargetPositionsComplete = true
	patch, err := orders.PlanPositionPatch(*c, current.PositionBaselines[posting.PostingNumber])
	if err != nil || patch == nil {
		t.Fatalf("plan owned position: %v", err)
	}
	c.Action, c.PositionPatch = orders.ActionMirrorPositionUpdate, patch
	c.CustomerOrder.OwnedFields = patch.Fields()
	decision.Target = &target
	p.Decisions = []orders.Decision{decision}
	return f, p, target
}

func assertPositionBaselineAdvanced(t *testing.T, f ordersStoreFixture, work orders.Work) {
	t.Helper()
	current, err := f.db.OrderReviewContext(f.ctx, f.config.Scope)
	if err != nil {
		t.Fatal(err)
	}
	baseline := current.PositionBaselines[work.Command.PostingNumber]
	if baseline.WorkID != work.ID || len(baseline.Bindings) != 1 || baseline.Bindings[0].Native.ID != "native-position" || baseline.Bindings[0].Source.Quantity != "1" {
		t.Fatal("reconciled patch did not retain exact native/source ownership")
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_write_intents`, 1)
}

func ackPositionPatch(t *testing.T, f ordersStoreFixture, work orders.Work) orders.Work {
	t.Helper()
	receipt := orders.Receipt{CommandID: work.Command.ID, State: orders.StateAcknowledged, HTTPStatus: 200, ExternalID: work.Command.Correlation.CustomerOrderID, ObservedAt: time.Now().UTC()}
	if err := f.db.CompleteOrderWork(f.ctx, orders.Completion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: orders.StateDispatching, State: orders.StateAcknowledged, Receipt: receipt}); err != nil {
		t.Fatal(err)
	}
	return ordersStoreClaim(t, f, time.Minute)
}

func initialPositionBaseline(t *testing.T) (ordersStoreFixture, orders.Posting, orders.TargetObservation) {
	t.Helper()
	f := newOrdersStoreFixture(t)
	posting := ordersStorePosting(f.config.Scope, "position-baseline", "source-old", time.Now().UTC())
	ordersStoreCommit(t, f, posting, "position-old")
	initial := ordersStorePreview(t, f, posting.PostingNumber)
	ordersStoreApprove(t, f, initial)
	first := ordersStoreClaim(t, f, time.Minute)
	target := scalarReadbackTarget(first)
	target.Positions[0].Reserve = orders.Present(orders.Decimal("0"))
	target.Positions[0].Shipped = orders.Present(orders.Decimal("0"))
	target.Positions[0].Discount = orders.Present(orders.Decimal("0"))
	if err := f.db.CompleteOrderWork(f.ctx, scalarReadbackCompletion(first, target)); err != nil {
		t.Fatal(err)
	}

	return f, posting, target
}
