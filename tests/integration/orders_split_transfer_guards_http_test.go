//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersSplitTransferFreshParentPreflightBlocksChangedNativeFrontier(t *testing.T) {
	f, _, split := retainedSplitParentFixture(t)
	reduceRetainedSplitParent(t, f, split, false)
	preview := splitMemberHTTPPreview(t, f, "split-http-child", nil)
	f.approve(t, preview)
	f.editForeignReserve(t)
	f.run(t, orders.StateBlocked)
	assertSplitTransferNeverDispatched(t, f)
}

func TestOrdersSplitTransferFreshParentPreflightBlocksChangedOzonParent(t *testing.T) {
	f, provider, split := retainedSplitParentFixture(t)
	reduceRetainedSplitParent(t, f, split, false)
	preview := splitMemberHTTPPreview(t, f, "split-http-child", nil)
	f.approve(t, preview)
	f.provider.base.server.Config.Handler = changedSplitParentHandler(t, provider)
	f.run(t, orders.StateBlocked)
	assertSplitTransferNeverDispatched(t, f)
}

func changedSplitParentHandler(t *testing.T, provider *unmirroredSplitHTTPProvider) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/posting/fbs/get" {
			provider.serve(w, r)
			return
		}
		recorded := httptest.NewRecorder()
		provider.serve(recorded, r)
		var body map[string]any
		if json.Unmarshal(recorded.Body.Bytes(), &body) != nil {
			t.Error("decode synthetic changed-parent response")
			w.WriteHeader(500)
			return
		}
		row, ok := body["result"].(map[string]any)
		if ok && row["posting_number"] == "position-http-posting" {
			row["status"], row["updated_at"] = "delivering", "2026-11-04T12:01:00Z"
		}
		provider.base.base.reply(w, body)
	})
}

func assertSplitTransferNeverDispatched(t *testing.T, f positionHTTPFixture) {
	t.Helper()
	work := f.latestWork(t)
	if work.Command.PostingNumber != "split-http-child" || work.Command.SplitTransfer == nil || work.DispatchCount != 0 || work.State != orders.StateBlocked {
		t.Fatal("stale parent permitted dependent child dispatch")
	}
	if f.provider.patches.Load() != 1 {
		t.Fatal("child preflight changed parent reserve again")
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if len(f.provider.base.native) != 1 {
		t.Fatal("blocked child created an external native document")
	}
}

func TestOrdersSplitTransferQueuedParentInvalidatesChildApproval(t *testing.T) {
	f, _, split := retainedSplitParentFixture(t)
	reduceRetainedSplitParent(t, f, split, false)
	addSplitParentDescriptionMapping(t, &f)
	child := splitMemberHTTPPreview(t, f, "split-http-child", nil)
	parent := splitMemberHTTPPreview(t, f, split.Command.PostingNumber, nil)
	if len(parent.Decisions[0].Commands) != 1 || parent.Decisions[0].Commands[0].Action != orders.ActionMirrorUpdate {
		t.Fatal("legitimate scalar parent mutation missing")
	}
	f.approve(t, parent)
	assertSplitTransferApprovalRefused(t, f, child)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE posting_number='split-http-child'`, 0)
}

func addSplitParentDescriptionMapping(t *testing.T, f *positionHTTPFixture) {
	t.Helper()
	config := f.config
	config.FieldMappings = append(config.FieldMappings, orders.FieldMapping{Source: "status", Target: "description"})
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "split-transfer-description-policy"}, time.Now())
	if err != nil {
		t.Fatal("save audited scalar parent policy", err)
	}
}

func assertSplitTransferApprovalRefused(t *testing.T, f positionHTTPFixture, preview orders.Preview) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "preview_id": preview.ID, "acknowledge": true, "idempotency_key": "refused-child-" + preview.ID})
	if err != nil {
		t.Fatal(err)
	}
	status, _ := f.merchant.request(t, http.MethodPost, "/api/orders/approve", string(body), true)
	if status != http.StatusConflict && status != http.StatusUnprocessableEntity {
		t.Fatalf("queued parent did not invalidate child approval: HTTP %d", status)
	}
}

func TestOrdersSplitTransferAcknowledgedChildFencesParentDispatch(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "acknowledged", true: "unknown"}[unknown], func(t *testing.T) {
			assertSplitTransferChildFencesParent(t, unknown)
		})
	}
}

func assertSplitTransferChildFencesParent(t *testing.T, unknown bool) {
	t.Helper()
	f, _, split := retainedSplitParentFixture(t)
	reduceRetainedSplitParent(t, f, split, false)
	addSplitParentDescriptionMapping(t, &f)
	f.provider.base.mu.Lock()
	f.provider.base.lost["split-http-child"] = unknown
	f.provider.base.mu.Unlock()
	f.approve(t, splitMemberHTTPPreview(t, f, "split-http-child", nil))
	state := orders.StateAcknowledged
	if unknown {
		state = orders.StateUnknown
	}
	f.run(t, state)
	child := f.latestWork(t)
	parent := splitMemberHTTPPreview(t, f, split.Command.PostingNumber, nil)
	f.approve(t, parent)
	held, err := f.db.ClaimOrderWork(f.ctx, orders.ClaimRequest{WorkerID: "split-child-poll-holder", Now: time.Now(), LeaseUntil: time.Now().Add(time.Minute)})
	if err != nil || held == nil || held.ID != child.ID || held.State != state {
		t.Fatal("hold acknowledged child polling lease", err)
	}
	f.run(t, orders.StateBlocked)
	work := f.latestWork(t)
	if work.Command.PostingNumber != split.Command.PostingNumber || work.DispatchCount != 0 || work.Command.Action != orders.ActionMirrorUpdate {
		t.Fatal("active dependent child permitted a parent mutation")
	}
	if f.provider.patches.Load() != 1 {
		t.Fatal("parent changed after dependent child dispatch")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE posting_number='split-http-child' AND state IN ('ACKNOWLEDGED','UNKNOWN') AND dispatch_count=1`, 1)
}
