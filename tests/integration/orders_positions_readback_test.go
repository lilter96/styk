//go:build integration

package integration

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersScalarReconciliationPreservesFrozenNativePositions(t *testing.T) {
	f, work, baseline := scalarReadbackFixture(t, true)
	for _, test := range []struct {
		name   string
		change func(*orders.TargetObservation)
	}{
		{"source quantity", func(v *orders.TargetObservation) { v.Positions[0].Quantity = "9" }},
		{"source price", func(v *orders.TargetObservation) {
			v.Positions[0].Price = orders.Present(orders.Money{Amount: "99", Currency: "RUB"})
		}},
		{"foreign reserve", func(v *orders.TargetObservation) { v.Positions[1].Reserve = orders.Present(orders.Decimal("3")) }},
		{"unknown foreign reserve", func(v *orders.TargetObservation) {
			v.Positions[1].Reserve = orders.Field[orders.Decimal]{Presence: orders.PresenceMissing}
		}},
		{"foreign line removed", func(v *orders.TargetObservation) { v.Positions = v.Positions[:1] }},
		{"duplicate native identity", func(v *orders.TargetObservation) { v.Positions[1].ID = v.Positions[0].ID }},
		{"foreign assortment", func(v *orders.TargetObservation) { v.Positions[1].MoyskladID = "other-manual-product" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed := baseline
			observed.Positions = slices.Clone(baseline.Positions)
			test.change(&observed)
			if err := f.db.CompleteOrderWork(f.ctx, scalarReadbackCompletion(work, observed)); !errors.Is(err, orders.ErrStale) {
				t.Fatalf("changed native facts reconciled: %v", err)
			}
			assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_reconciliations`, 1)
		})
	}
	baseline.Positions[1].Reserve = orders.Present(orders.Decimal("2.000"))
	slices.Reverse(baseline.Positions)
	if err := f.db.CompleteOrderWork(f.ctx, scalarReadbackCompletion(work, baseline)); err != nil {
		t.Fatal(err)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_reconciliations`, 2)
}

func TestOrdersScalarReconciliationRequiresCompletePositionBaseline(t *testing.T) {
	f, work, target := scalarReadbackFixture(t, false)
	if err := f.db.CompleteOrderWork(f.ctx, scalarReadbackCompletion(work, target)); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("unknown position baseline reconciled: %v", err)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_reconciliations`, 1)
}

func scalarReadbackFixture(t *testing.T, complete bool) (ordersStoreFixture, orders.Work, orders.TargetObservation) {
	t.Helper()
	f := newOrdersStoreFixture(t)
	posting := ordersStorePosting(f.config.Scope, "scalar-posting", "source-1", time.Now().UTC())
	ordersStoreCommit(t, f, posting, "scalar-source")
	p := ordersStorePreview(t, f, posting.PostingNumber)
	ordersStoreApprove(t, f, p)
	first := ordersStoreClaim(t, f, time.Minute)
	target := scalarReadbackTarget(first)
	if err := f.db.CompleteOrderWork(f.ctx, scalarReadbackCompletion(first, target)); err != nil {
		t.Fatal(err)
	}
	target.Positions = append(target.Positions, orders.TargetPosition{ID: "manual-position", MoyskladKind: "product", MoyskladID: "manual-product", Quantity: "7", Reserve: orders.Present(orders.Decimal("2"))})
	p.ID += "-update"
	c := &p.Decisions[0].Commands[0]
	c.ID += "-update"
	c.IdempotencyKey = c.ID
	c.Action = orders.ActionMirrorUpdate
	c.Correlation = target.Correlation
	c.ExpectedTargetRevision = target.Revision
	c.TargetPositions = slices.Clone(target.Positions)
	c.TargetPositionsComplete = complete
	c.CustomerOrder.OwnedFields = []string{"name"}
	p.Decisions[0].Correlation = target.Correlation
	p.Decisions[0].Target = &target
	p, err := f.db.SaveOrderPreview(f.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	ordersStoreApprove(t, f, p)
	return f, ordersStoreClaim(t, f, time.Minute), target
}

func scalarReadbackTarget(work orders.Work) orders.TargetObservation {
	correlation := work.Command.Correlation
	correlation.CustomerOrderID = "native-scalar-order"
	position := work.Command.CustomerOrder.Positions[0]
	return orders.TargetObservation{Scope: work.Scope, Correlation: correlation, Revision: "native-revision", Fields: *work.Command.CustomerOrder, Complete: true, ObservedAt: time.Now().UTC(), Positions: []orders.TargetPosition{{ID: "native-position", Identity: position.Identity, MoyskladKind: position.Mapping.MoyskladKind, MoyskladID: position.Mapping.MoyskladID, Quantity: position.Quantity, Reserve: position.Reserve, Price: position.Price, VAT: position.VAT}}}
}

func scalarReadbackCompletion(work orders.Work, target orders.TargetObservation) orders.Completion {
	return orders.Completion{WorkID: work.ID, Scope: work.Scope, ClaimToken: work.ClaimToken, ExpectedState: work.State, State: orders.StateReconciled, ObservedTarget: &target}
}
