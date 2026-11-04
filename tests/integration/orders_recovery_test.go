//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/orders"
)

type ordersRecoveryHTTPFixture struct {
	positionHTTPFixture
	unknown  orders.Work
	original string
}

func newOrdersRecoveryHTTPFixture(t *testing.T) ordersRecoveryHTTPFixture {
	t.Helper()
	f, preview := positionHTTPUpdate(t)
	f.approve(t, preview)
	f.provider.losePatchResponse.Store(true)
	f.run(t, orders.StateUnknown)
	work := f.latestWork(t)
	f.provider.change(3, 1999)
	blocked := f.preview(t)
	if blocked.Decisions[0].Ready {
		t.Fatal("UNKNOWN fence did not isolate its posting")
	}
	var original string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT to_jsonb(w)::text FROM orders_work w WHERE id=$1`, work.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	result := ordersRecoveryHTTPFixture{positionHTTPFixture: f, unknown: work, original: original}
	result.attachAPI(t)
	return result
}

func (f *ordersRecoveryHTTPFixture) attachAPI(t *testing.T) {
	t.Helper()
	handler, err := console.MerchantLoopbackTestHandler(f.db, "http://127.0.0.1:5173", &testMailbox{tokens: map[string]string{}}, f.provider.base.server.URL, f.provider.base.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f.merchant = &merchantBrowser{client: f.merchant.client, csrf: f.merchant.csrf, base: server.URL}
}

func (f ordersRecoveryHTTPFixture) body(t *testing.T, extra map[string]any) string {
	t.Helper()
	value := map[string]any{"tenant": f.config.Scope.Tenant, "moysklad_account": f.config.Scope.MoyskladAccount, "ozon_account": f.config.Scope.OzonAccount, "work_id": f.unknown.ID}
	for key, item := range extra {
		value[key] = item
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (f ordersRecoveryHTTPFixture) check(t *testing.T) orders.RecoveryView {
	t.Helper()
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/check", f.body(t, nil), true)
	var view orders.RecoveryView
	if status != http.StatusOK || json.Unmarshal(raw, &view) != nil || view.Evidence == nil {
		_, detail := f.service.CheckRecovery(f.ctx, orders.RecoveryRequest{Scope: f.config.Scope, WorkID: f.unknown.ID, Actor: f.session.AuditID})
		t.Fatalf("recovery check: HTTP %d %s, direct error=%v", status, raw, detail)
	}
	return view
}

func (f ordersRecoveryHTTPFixture) resolve(t *testing.T, evidence, key string) orders.RecoveryView {
	t.Helper()
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", f.body(t, map[string]any{"evidence_id": evidence, "idempotency_key": key, "reason": "Operator accepts possible delayed effects for this exact test target", "acknowledge": true, "worker_stopped": true}), true)
	var view orders.RecoveryView
	if status != http.StatusOK || json.Unmarshal(raw, &view) != nil || view.Resolution == nil {
		t.Fatalf("audited recovery: HTTP %d", status)
	}
	return view
}

func (f ordersRecoveryHTTPFixture) assertOriginal(t *testing.T) {
	t.Helper()
	var current, state string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT to_jsonb(w)::text FROM orders_work w WHERE id=$1`, f.unknown.ID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != f.original {
		t.Fatal("original UNKNOWN work was rewritten")
	}
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT state FROM outbox_tasks WHERE orders_work_id=$1`, f.unknown.ID).Scan(&state); err != nil || state != "failed" {
		t.Fatal("superseded readback task still runnable")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_resolutions`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_evidence WHERE evidence::text LIKE '%PRIVATE_%'`, 0)
}

func TestOrdersRecoveryKnownNativeUpdateCreatesFreshLinkedDecision(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	checked := f.check(t)
	if !checked.Eligible || checked.Evidence.Target == nil || checked.Work.State != orders.StateUnknown {
		t.Fatalf("fresh known-native evidence not eligible: %+v", checked.Blockers)
	}
	resolved := f.resolve(t, checked.Evidence.ID, "recovery-first")
	if resolved.Resolution.Attestation == nil || resolved.Resolution.NativeID != f.unknown.Command.Correlation.CustomerOrderID {
		t.Fatal("exact target attestation missing")
	}
	f.assertOriginal(t)
	preview := f.preview(t)
	if len(preview.Decisions[0].Commands) != 1 {
		t.Fatalf("fresh decision blocked: %+v", preview.Decisions[0].Validation)
	}
	command := preview.Decisions[0].Commands[0]
	if command.Action != orders.ActionMirrorPositionUpdate || command.PositionPatch == nil || command.PositionPatch.Baseline.EvidenceClass != "OPERATOR_ATTESTED" || command.PositionPatch.Baseline.RecoveryID != resolved.Resolution.Attestation.ID {
		t.Fatal("recovery was disguised as provider-verified baseline")
	}
	operation := f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StateReconciled)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_links`, 1)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_write_intents`, 3)
	if f.provider.patches.Load() != 2 {
		t.Fatal("old external intent was replayed")
	}
	f.assertOriginal(t)
	q := url.Values{"tenant": {f.config.Scope.Tenant}, "moysklad_account": {f.config.Scope.MoyskladAccount}, "ozon_account": {f.config.Scope.OzonAccount}, "work_id": {f.unknown.ID}}
	status, raw := f.merchant.request(t, http.MethodGet, "/api/orders/recovery?"+q.Encode(), "", false)
	var history orders.RecoveryView
	if status != 200 || json.Unmarshal(raw, &history) != nil || history.Resolution.NewOperationID != operation.ID || history.Work.State != orders.StateUnknown {
		t.Fatal("historical resolution link lost")
	}
}

func TestOrdersRecoveryEvidenceExcludesNativeFreeText(t *testing.T) {
	f := newOrdersRecoveryHTTPFixture(t)
	p := f.provider.base
	p.mu.Lock()
	for _, native := range p.native {
		native.body["description"] = "PRIVATE_NATIVE_DESCRIPTION"
		native.body["shipmentAddress"] = "PRIVATE_NATIVE_ADDRESS"
	}
	p.mu.Unlock()
	checked := f.check(t)
	if !checked.Eligible {
		t.Fatalf("private readback unavailable: %+v", checked.Blockers)
	}
	raw, err := json.Marshal(checked)
	if err != nil || strings.Contains(string(raw), "PRIVATE_NATIVE") {
		t.Fatal("native private fields exposed in public recovery")
	}
	body := f.body(t, map[string]any{"evidence_id": checked.Evidence.ID, "idempotency_key": "recovery-private-reason", "reason": "PRIVATE_BUYER_REASON_SENTINEL", "acknowledge": true, "worker_stopped": true})
	status, raw := f.merchant.request(t, http.MethodPost, "/api/orders/recovery/resolve", body, true)
	if status != 200 || strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("private reason exposed")
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_recovery_reason_secrets WHERE position('PRIVATE_'::bytea IN ciphertext)>0`, 0)
	f.assertOriginal(t)
}
