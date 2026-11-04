//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orderactions"
	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersNestedSplitCannotForgetAncestorReserve(t *testing.T) {
	f, root := ancestryFixture(t)
	root.Lines[0].Quantity = "4"
	ordersStoreCommit(t, f, root, "ancestry-root")
	ancestryMirrorReserve(t, f, root)
	children := ancestryReconcileSplit(t, f, root, "ancestry-child", "2")
	child := children[1]
	child.Status = orders.Present("awaiting_packaging")
	child.Evidence.Revision = "child-provider-correction"
	child.Evidence.ObservedAt = time.Now().UTC()
	child.Evidence.ProviderUpdatedAt = orders.Present(child.Evidence.ObservedAt)
	ordersStoreCommit(t, f, child, "ancestry-child-correction")
	grandchildren := ancestryReconcileSplit(t, f, child, "ancestry-grandchild", "1")
	review, err := f.db.OrderReviewContextFor(f.ctx, f.config.Scope, []string{grandchildren[1].PostingNumber}, false)
	if err != nil {
		t.Fatal(err)
	}
	number := grandchildren[1].PostingNumber
	if !review.SplitReserveBlocked[number] || review.SplitReservePermissions[number].SplitWorkID != "" {
		t.Fatalf("nested split forgot held ancestor reserve: blocked=%v permissions=%v", review.SplitReserveBlocked, review.SplitReservePermissions)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work WHERE action='mirror_create' AND dispatch_count=1 AND state='RECONCILED'`, 1)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work WHERE posting_number='ancestry-child' AND action LIKE 'mirror_%'`, 0)
	assertAncestryProviderParentAbsent(t, f, number)
}

func TestOrdersIndependentUnmirroredSplitCanAuthorizeChildReserve(t *testing.T) {
	f, root := ancestryFixture(t)
	ordersStoreCommit(t, f, root, "unmirrored-root")
	children := ancestryReconcileSplit(t, f, root, "unmirrored-child", "1")
	review, err := f.db.OrderReviewContextFor(f.ctx, f.config.Scope, []string{children[1].PostingNumber}, false)
	if err != nil {
		t.Fatal(err)
	}
	permission := review.SplitReservePermissions[children[1].PostingNumber]
	if review.SplitReserveBlocked[children[1].PostingNumber] || !permission.Allows(children[1]) {
		t.Fatalf("independent unmirrored root not eligible: blocked=%v permission=%+v", review.SplitReserveBlocked, permission)
	}
	assertAncestryProviderParentAbsent(t, f, children[1].PostingNumber)
}

func ancestryFixture(t *testing.T) (ordersStoreFixture, orders.Posting) {
	t.Helper()
	f := newOrdersStoreFixture(t)
	config := f.config
	config.FieldMappings = append(config.FieldMappings, orders.FieldMapping{Source: orders.ReserveOpenQuantitySource, Target: "positions.reserve"}, orders.FieldMapping{Source: "constant.true", Target: "applicable"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "ancestry-reserve-config"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return f, ordersStorePosting(f.config.Scope, "ancestry-root", "root-source", time.Now().UTC())
}

func ancestryMirrorReserve(t *testing.T, f ordersStoreFixture, root orders.Posting) {
	t.Helper()
	preview := ordersStorePreview(t, f, root.PostingNumber)
	preview.ID += "-reserve"
	command := &preview.Decisions[0].Commands[0]
	command.ID += "-reserve"
	command.IdempotencyKey = command.ID
	command.CustomerOrder.Applicable = orders.Present(true)
	command.CustomerOrder.Positions[0].Reserve = orders.Present(root.Lines[0].Quantity)
	var err error
	preview, err = f.db.SaveOrderPreview(f.ctx, preview)
	if err != nil {
		t.Fatal(err)
	}
	ordersStoreApprove(t, f, preview)
	work := ordersStoreClaim(t, f, time.Minute)
	ancestryBegin(t, f, work)
	work = ackPositionPatch(t, f, work)
	target := scalarReadbackTarget(work)
	target.Positions[0].Shipped = orders.Present(orders.Decimal("0"))
	target.Positions[0].Discount = orders.Present(orders.Decimal("0"))
	if err = f.db.CompleteOrderWork(f.ctx, scalarReadbackCompletion(work, target)); err != nil {
		t.Fatal(err)
	}
}

func ancestryBegin(t *testing.T, f ordersStoreFixture, work orders.Work) {
	t.Helper()
	allowed, err := f.db.GuardOrderDispatch(f.ctx, orders.DispatchGuard{Work: work, Begin: true, Now: time.Now()})
	if err != nil || !allowed {
		t.Fatalf("actual declared dispatch blocked: %v %v", allowed, err)
	}
}

func ancestryReconcileSplit(t *testing.T, f ordersStoreFixture, before orders.Posting, childNumber string, quantity orders.Decimal) []orders.Posting {
	t.Helper()
	preview := ancestrySplitPreview(t, f, before, quantity)
	ordersStoreApprove(t, f, preview)
	work := ordersStoreClaim(t, f, time.Minute)
	ancestryBegin(t, f, work)
	receipt := orders.Receipt{CommandID: work.Command.ID, State: orders.StateAcknowledged, PostingNumbers: []string{before.PostingNumber, childNumber}, HTTPStatus: 200, ObservedAt: time.Now().UTC()}
	if err := f.db.CompleteOrderWork(f.ctx, orders.Completion{WorkID: work.ID, Scope: work.Scope, ClaimToken: work.ClaimToken, ExpectedState: orders.StateDispatching, State: orders.StateAcknowledged, Receipt: receipt}); err != nil {
		t.Fatal(err)
	}
	work = ordersStoreClaim(t, f, time.Minute)
	children := ancestrySplitChildren(before, receipt.PostingNumbers, quantity)
	receipt.State, receipt.ObservedPostings = orders.StateReconciled, children
	if err := f.db.CompleteOrderWork(f.ctx, orders.Completion{WorkID: work.ID, Scope: work.Scope, ClaimToken: work.ClaimToken, ExpectedState: work.State, State: orders.StateReconciled, Receipt: receipt}); err != nil {
		t.Fatal(err)
	}
	return children
}

func ancestrySplitChildren(before orders.Posting, numbers []string, quantity orders.Decimal) []orders.Posting {
	children := make([]orders.Posting, len(numbers))
	for i, number := range numbers {
		child := before
		child.PostingNumber = number
		child.Lines = append([]orders.PostingLine(nil), before.Lines...)
		child.Lines[0].Quantity = quantity
		child.Status = orders.Present("awaiting_deliver")
		child.Evidence.ObservedAt = time.Now().UTC()
		child.Evidence.CheckedAt = child.Evidence.ObservedAt
		child.Evidence.ProviderUpdatedAt = orders.Present(child.Evidence.ObservedAt)
		child.Evidence.Revision = fmt.Sprintf("split-%s-%s", number, quantity)
		children[i] = child
	}
	return children
}

func assertAncestryProviderParentAbsent(t *testing.T, f ordersStoreFixture, number string) {
	t.Helper()
	page, err := f.db.OrderPostings(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10, Search: number})
	if err != nil || len(page.Postings) != 1 || page.Postings[0].ParentPostingNumber.IsKnown() {
		t.Fatal("local ancestry must not forge provider parent field", err)
	}
}

func ancestrySplitPreview(t *testing.T, f ordersStoreFixture, before orders.Posting, quantity orders.Decimal) orders.Preview {
	t.Helper()
	current, err := f.db.OrderReviewContext(f.ctx, f.config.Scope)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("ancestry-split-%d", now.UnixNano())
	decision := ordersStoreDecision(t, current, before.PostingNumber, id, now)
	command := &decision.Commands[0]
	command.Action, command.CustomerOrder = orders.ActionPostingSplit, nil
	cap := orders.CapabilityRef{ID: "synthetic-capability", Revision: 1, WarehouseID: *before.WarehouseID.Value, DeliveryMethodID: *before.DeliveryMethodID.Value, Scheme: "FBS", ObservedAt: now, ValidUntil: now.Add(time.Hour)}
	command.Warehouse.Capability = cap
	line := orders.ActionQuantity{Identity: before.Lines[0].Identity, Quantity: quantity}
	command.PostingAction = &orders.PostingAction{Capability: cap, ContractHash: orderactions.ExecutionContractHash(), Parameters: orders.ActionParameters{Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{line}}, {Lines: []orders.ActionQuantity{line}}}, ReadinessConfirmed: true}}
	preview := orders.Preview{ID: id, Scope: f.config.Scope, ConfigRevision: current.Config.Revision, Credentials: current.Credentials, Actor: f.session.AuditID, CreatedAt: now, ExpiresAt: now.Add(time.Minute), Decisions: []orders.Decision{decision}}
	preview, err = f.db.SaveOrderPreview(f.ctx, preview)
	if err != nil {
		t.Fatal("save split", err)
	}
	return preview
}

func TestOrdersSplitReserveUsesReviewedParentInsteadOfAbbreviatedRaw(t *testing.T) {
	t.Parallel()
	f, source := ancestryFixture(t)
	source.ParentPostingNumber = orders.Present("provider-ancestor")
	ordersStoreCommit(t, f, source, "known-provider-parent")
	source.ParentPostingNumber = orders.Field[string]{Presence: orders.PresenceMissing}
	source.Evidence.Revision = "abbreviated-parent-omitted"
	source.Evidence.ObservedAt = time.Now().UTC()
	source.Evidence.ProviderUpdatedAt = orders.Present(source.Evidence.ObservedAt)
	ordersStoreCommit(t, f, source, "abbreviated-provider-parent")
	current, err := f.db.OrderReviewContextFor(f.ctx, f.config.Scope, []string{source.PostingNumber}, false)
	if err != nil || len(current.Postings) != 1 {
		t.Fatal("read merged reviewed facts", err)
	}
	reviewed := current.Postings[0]
	if !reviewed.ParentPostingNumber.IsKnown() || *reviewed.ParentPostingNumber.Value != "provider-ancestor" || reviewed.Evidence.Revision != source.Evidence.Revision {
		t.Fatal("abbreviated source must retain known parent in reviewed projection")
	}
	var rawParent string
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT posting#>>'{parent_posting_number,presence}' FROM orders_posting_observations WHERE source_revision=$1`, source.Evidence.Revision).Scan(&rawParent); err != nil || rawParent != "missing" {
		t.Fatal("fixture must preserve actual omitted raw field", err)
	}
	children := ancestryReconcileSplit(t, f, reviewed, "provider-ancestor-child", "1")
	review, err := f.db.OrderReviewContextFor(f.ctx, f.config.Scope, []string{children[1].PostingNumber}, false)
	if err != nil {
		t.Fatal(err)
	}
	number := children[1].PostingNumber
	if !review.SplitReserveBlocked[number] || review.SplitReservePermissions[number].SplitWorkID != "" {
		t.Fatalf("raw omission erased reviewed ancestry: blocked=%v permissions=%v", review.SplitReserveBlocked, review.SplitReservePermissions)
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work WHERE action='posting_split' AND state='RECONCILED' AND dispatch_count=1`, 1)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work WHERE action LIKE 'mirror_%'`, 0)
}
