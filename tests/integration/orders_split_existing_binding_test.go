//go:build integration

package integration

import (
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orderactions"
	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersSplitExistingParentBindingWithoutDispatchStillBlocksChildReserve(t *testing.T) {
	f := newOrdersStoreFixture(t)
	before := ordersStorePosting(f.config.Scope, "split-existing-parent", "source-before-split", time.Now().UTC())
	ordersStoreCommit(t, f, before, "existing-parent-source")
	mirror := ordersStorePreview(t, f, before.PostingNumber)
	ordersStoreApprove(t, f, mirror)
	work := ordersStoreClaim(t, f, time.Minute)
	target := scalarReadbackTarget(work)
	target.Correlation.CustomerOrderID = "00000000-0000-4000-8000-000000000096"
	target.Fields.Applicable = orders.Present(true)
	target.Positions[0].Reserve = orders.Present(orders.Decimal("2"))
	target.Positions[0].Shipped = orders.Present(orders.Decimal("0"))
	// This is the normal matching-marker read-only fast path. There is an
	// existing native document/reserve, but no external mutation to count.
	if err := f.db.CompleteOrderWork(f.ctx, scalarReadbackCompletion(work, target)); err != nil {
		t.Fatal("reconcile existing native parent without dispatch", err)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work WHERE dispatch_count=0 AND state='RECONCILED'`, 1)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_write_intents`, 0)
	config := f.config
	config.FieldMappings = append(config.FieldMappings, orders.FieldMapping{Source: orders.ReserveOpenQuantitySource, Target: "positions.reserve"}, orders.FieldMapping{Source: "constant.true", Target: "applicable"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "existing-binding-reserve-policy"}, time.Now())
	if err != nil {
		t.Fatal("save explicit reserve policy", err)
	}
	preview := splitExistingBindingPreview(t, f, mirror, target.Correlation)
	splitExistingBindingReconcile(t, f, preview, before)
	review, err := f.db.OrderReviewContextFor(f.ctx, f.config.Scope, []string{"split-existing-child"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !review.SplitReserveBlocked["split-existing-child"] || review.SplitReservePermissions["split-existing-child"].SplitWorkID != "" {
		t.Fatal("zero-dispatch reconciliation was mistaken for absence of native parent reserve")
	}
}

func splitExistingBindingPreview(t *testing.T, f ordersStoreFixture, original orders.Preview, correlation orders.Correlation) orders.Preview {
	t.Helper()
	preview := original
	preview.ID += "-split-existing-binding"
	preview.ConfigRevision = f.config.Revision
	preview.Decisions = slices.Clone(original.Decisions)
	decision := &preview.Decisions[0]
	decision.Correlation = correlation
	decision.Commands = slices.Clone(decision.Commands)
	command := &decision.Commands[0]
	command.ID += "-split-existing-binding"
	command.IdempotencyKey = command.ID
	command.Action, command.Correlation = orders.ActionPostingSplit, correlation
	command.ConfigRevision, command.CustomerOrder = f.config.Revision, nil
	capability := orders.CapabilityRef{ID: "synthetic-capability", Revision: 1, WarehouseID: *decision.Posting.WarehouseID.Value, DeliveryMethodID: *decision.Posting.DeliveryMethodID.Value, Scheme: "FBS", ObservedAt: time.Now().UTC(), ValidUntil: time.Now().UTC().Add(time.Hour)}
	command.Warehouse.Capability = capability
	line := orders.ActionQuantity{Identity: decision.Posting.Lines[0].Identity, Quantity: "1"}
	command.PostingAction = &orders.PostingAction{Capability: capability, ContractHash: orderactions.ExecutionContractHash(), Parameters: orders.ActionParameters{Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{line}}, {Lines: []orders.ActionQuantity{line}}}, ReadinessConfirmed: true}}
	preview, err := f.db.SaveOrderPreview(f.ctx, preview)
	if err != nil {
		t.Fatal("freeze split with existing native parent correlation", err)
	}
	if preview.Decisions[0].Correlation.CustomerOrderID == "" {
		t.Fatal("fixture lost existing native parent identity")
	}
	return preview
}

func splitExistingBindingReconcile(t *testing.T, f ordersStoreFixture, preview orders.Preview, before orders.Posting) {
	t.Helper()
	ordersStoreApprove(t, f, preview)
	work := ordersStoreClaim(t, f, time.Minute)
	allowed, err := f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: work, Begin: true, Now: time.Now()})
	if err != nil || !allowed {
		t.Fatal("split dispatch", allowed, err)
	}
	receipt := orders.Receipt{CommandID: work.Command.ID, State: orders.StateAcknowledged, PostingNumbers: []string{before.PostingNumber, "split-existing-child"}, HTTPStatus: 200, ObservedAt: time.Now().UTC()}
	if err := f.db.CompleteOrderWork(f.ctx, orders.Completion{WorkID: work.ID, Scope: work.Scope, ClaimToken: work.ClaimToken, ExpectedState: orders.StateDispatching, State: orders.StateAcknowledged, Receipt: receipt}); err != nil {
		t.Fatal("split acknowledgment", err)
	}
	work = ordersStoreClaim(t, f, time.Minute)
	children := make([]orders.Posting, 0, len(receipt.PostingNumbers))
	for _, number := range receipt.PostingNumbers {
		child := before
		child.PostingNumber = number
		child.Lines = slices.Clone(before.Lines)
		child.Lines[0].Quantity = "1"
		child.Evidence.ObservedAt = time.Now().UTC()
		child.Evidence.CheckedAt = child.Evidence.ObservedAt
		child.Evidence.ProviderUpdatedAt = orders.Present(child.Evidence.ObservedAt)
		child.Evidence.Revision = "split-result-" + number
		children = append(children, child)
	}
	receipt.State, receipt.ObservedPostings = orders.StateReconciled, children
	if err := f.db.CompleteOrderWork(f.ctx, orders.Completion{WorkID: work.ID, Scope: work.Scope, ClaimToken: work.ClaimToken, ExpectedState: work.State, State: orders.StateReconciled, Receipt: receipt}); err != nil {
		t.Fatal("split child readback", err)
	}
}
