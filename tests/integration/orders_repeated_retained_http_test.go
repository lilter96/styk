//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func repeatedRetainedFixture(t *testing.T) (positionHTTPFixture, *repeatedRetainedHTTPProvider, orders.TargetObservation, string) {
	t.Helper()
	f, _, _ := unmirroredSplitFixture(t)
	p := &repeatedRetainedHTTPProvider{base: f.provider}
	f.provider.base.server.Config.Handler = http.HandlerFunc(p.serve)
	f.approve(t, f.preview(t))
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	root := f.readNative(t)
	if len(root.Positions) != 1 || root.Positions[0].ID == "" {
		t.Fatal("missing original root native position identity")
	}
	origin := f.latestWork(t).ID
	f.addForeignLine(t)
	return f, p, root, origin
}

func repeatedRetainedHeld(t *testing.T, f positionHTTPFixture, child string) {
	t.Helper()
	held := unmirroredUnassembledHTTPReview(t, f, child)
	if held.Decisions[0].Ready || len(held.Decisions[0].Commands) != 0 {
		t.Fatal("descendant reserve released before actual latest retained reduction readback")
	}
}

func repeatedRetainedReduce(t *testing.T, f positionHTTPFixture, child string, root orders.TargetObservation, previous string) (orders.Preview, orders.Operation, string) {
	t.Helper()
	repeatedRetainedHeld(t, f, child)
	preview := splitMemberHTTPPreview(t, f, nestedRoot, nil)
	c := preview.Decisions[0].Commands[0]
	if source := preview.Decisions[0].Posting; source.ParentPostingNumber.Presence != orders.PresenceMissing || source.ParentPostingNumber.Value != nil {
		t.Fatal("local retained history fabricated a provider parent")
	}
	if c.Action != orders.ActionMirrorPositionUpdate || c.PositionPatch == nil || !c.PositionPatch.Quantity.IsKnown() || !c.PositionPatch.Reserve.IsKnown() || *c.PositionPatch.Quantity.Value != preview.Decisions[0].Posting.Lines[0].Quantity || *c.PositionPatch.Reserve.Value != preview.Decisions[0].Posting.Lines[0].Quantity || c.Correlation.CustomerOrderID != root.Correlation.CustomerOrderID || c.PositionPatch.PositionID != root.Positions[0].ID || c.PositionPatch.Baseline.WorkID != previous {
		t.Fatal("retained reduction did not use latest exact native document/position frontier")
	}
	operation := f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	repeatedRetainedHeld(t, f, child)
	f.run(t, orders.StateReconciled)
	current := f.readNative(t)
	if current.Correlation.CustomerOrderID != root.Correlation.CustomerOrderID {
		t.Fatal("retained split recreated original customerorder")
	}
	assertRepeatedRetainedPosition(t, current, root.Positions[0].ID, *c.PositionPatch.Quantity.Value)
	return preview, operation, f.latestWork(t).ID
}

func TestOrdersRepeatedRetainedRootKeepsNativeIdentityAcrossThreeSplits(t *testing.T) {
	f, p, root, previous := repeatedRetainedFixture(t)
	var old orders.Preview
	var oldOperation orders.Operation
	for i, stage := range []struct {
		child    string
		quantity orders.Decimal
	}{{nestedChild, "4"}, {repeatedRetainedSecond, "2"}, {repeatedRetainedThird, "1"}} {
		t.Logf("repeated retained generation %d: split, reduce, then admit", i+1)
		nestedSettlementSplit(t, f, nestedRoot, stage.quantity)
		preview, operation, next := repeatedRetainedReduce(t, f, stage.child, root, previous)
		if i == 0 {
			old, oldOperation = preview, operation
		}
		previous = next
		if i == 1 {
			f.runner = f.restartedRunner(t)
		}
		deepOwnedMirror(t, f, stage.child)
	}
	assertDeepOwnedConserved(t, f)
	for _, number := range []string{nestedRoot, repeatedRetainedThird} {
		if next := splitMemberHTTPPreview(t, f, number, nil); len(next.Decisions[0].Commands) != 0 {
			t.Fatal("unchanged retained allocation created another operation")
		}
	}
	repeatedRetainedOldApproval(t, f, old, oldOperation.ID)
	if p.attempts.Load() != 3 || p.writes.Load() != 3 || f.provider.patches.Load() != 3 {
		t.Fatal("repeated retained operation was resent")
	}
}

func repeatedRetainedOldApproval(t *testing.T, f positionHTTPFixture, old orders.Preview, operationID string) {
	t.Helper()
	var before int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_operations WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, f.config.Scope.Tenant, f.config.Scope.MoyskladAccount, f.config.Scope.OzonAccount).Scan(&before); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "preview_id": old.ID, "acknowledge": true, "idempotency_key": "old-retained-proof-new-intent"})
	if err != nil {
		t.Fatal(err)
	}
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/approve", string(body), true)
	if status == http.StatusOK {
		var replay orders.Operation
		if json.Unmarshal(raw, &replay) != nil || replay.ID != operationID {
			t.Fatal("stale retained review created a new operation")
		}
	} else if status != http.StatusConflict {
		t.Fatalf("unexpected stale retained approval response %d", status)
	}
	var after int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_operations WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, f.config.Scope.Tenant, f.config.Scope.MoyskladAccount, f.config.Scope.OzonAccount).Scan(&after); err != nil || after != before {
		t.Fatal("stale review changed durable operation count", err)
	}
}

func TestOrdersRepeatedRetainedUnknownReductionDoesNotReuseEarlierRelease(t *testing.T) {
	f, p, root, previous := repeatedRetainedFixture(t)
	for _, stage := range []struct {
		child    string
		quantity orders.Decimal
	}{{nestedChild, "4"}, {repeatedRetainedSecond, "2"}} {
		nestedSettlementSplit(t, f, nestedRoot, stage.quantity)
		_, _, previous = repeatedRetainedReduce(t, f, stage.child, root, previous)
		deepOwnedMirror(t, f, stage.child)
	}
	nestedSettlementSplit(t, f, nestedRoot, "1")
	reduce := splitMemberHTTPPreview(t, f, nestedRoot, nil)
	if c := reduce.Decisions[0].Commands[0]; c.PositionPatch == nil || c.PositionPatch.Baseline.WorkID != previous {
		t.Fatal("third reduction reused older reserve baseline")
	}
	f.approve(t, reduce)
	f.provider.losePatchResponse.Store(true)
	f.run(t, orders.StateUnknown)
	unknown := f.latestWork(t)
	for _, number := range []string{nestedRoot, repeatedRetainedThird} {
		repeatedRetainedHeld(t, f, number)
	}
	repeatedRetainedIndependentReady(t, f)
	if work := f.latestWork(t); work.ID != unknown.ID || work.State != orders.StateUnknown || work.DispatchCount != 1 || p.attempts.Load() != 3 || p.writes.Load() != 3 || f.provider.patches.Load() != 3 {
		t.Fatal("UNKNOWN retained reduction was rewritten or blindly replayed")
	}
}

func repeatedRetainedIndependentReady(t *testing.T, f positionHTTPFixture) {
	t.Helper()
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	independent, err := binding.Client.ReadOrderPosting(f.ctx, f.config.Scope, repeatedRetainedIndependent)
	if err != nil {
		t.Fatal(err)
	}
	ordersStoreCommit(t, f.ordersStoreFixture, independent, "repeated-retained-independent")
	splitMemberHTTPPreview(t, f, repeatedRetainedIndependent, nil)
}

func assertRepeatedRetainedPosition(t *testing.T, current orders.TargetObservation, positionID string, quantity orders.Decimal) {
	t.Helper()
	for _, position := range current.Positions {
		if position.ID == positionID {
			if position.Quantity != quantity || !position.Reserve.IsKnown() || *position.Reserve.Value != quantity {
				t.Fatal("latest retained native frontier changed quantity or reserve")
			}
			return
		}
	}
	t.Fatal("retained source position UUID was replaced")
}
