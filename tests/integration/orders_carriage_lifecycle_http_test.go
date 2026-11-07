//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
)

// Synthetic provider responses retain the authenticated response structure.
// This fixture proves the application path; it is not production-write evidence.
type carriageLifecycleHTTP struct {
	*carriageReadHTTPFixture
	phase           atomic.Int32
	creates         atomic.Int32
	approves        atomic.Int32
	loseCreate      atomic.Bool
	loseApprove     atomic.Bool
	cancelledMember atomic.Bool
	quantity        atomic.Int64
}

func newCarriageLifecycleHTTP(t *testing.T) *carriageLifecycleHTTP {
	t.Helper()
	f := &carriageLifecycleHTTP{carriageReadHTTPFixture: newCarriageReadHTTPFixture(t)}
	f.quantity.Store(2)
	f.provider.base.server.Config.Handler = http.HandlerFunc(f.serveLifecycle)
	return f
}
func (f *carriageLifecycleHTTP) reply(w http.ResponseWriter, v any) { f.provider.base.reply(w, v) }
func (f *carriageLifecycleHTTP) serveLifecycle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Client-Id") != f.config.Scope.OzonAccount || r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+f.config.Scope.OzonAccount {
		w.WriteHeader(403)
		return
	}
	switch r.URL.Path {
	case "/v2/carriage/delivery/list":
		method := map[string]any{"delivery_method_id": 4001, "warehouse_id": 3001, "departure_date": "2026-11-07", "delivery_method_status": "active", "is_rfbs": false, "first_mile_changing": false, "first_mile_type": "dropoff", "integration_type": "ozon", "mandatory_packaged_count": 1, "optional_packaged_count": 0, "carriages": []any{}, "errors": []any{}}
		if f.phase.Load() == 1 {
			method["carriages"] = []any{map[string]any{"id": 5001, "status": "new"}}
		}
		f.reply(w, map[string]any{"methods": []any{method}, "cursor": "", "has_next": false})
	case "/v4/posting/fbs/unfulfilled/list":
		f.reply(w, map[string]any{"postings": []any{map[string]any{"posting_number": "carriage-http-posting"}}, "has_next": false, "cursor": ""})
	case "/v3/posting/fbs/get":
		row := f.provider.base.posting("carriage-http-posting", "FBS")
		row["order_id"] = 5001
		row["delivery_method"] = map[string]any{"id": 4001, "warehouse_id": 3001}
		product := row["products"].([]any)[0].(map[string]any)
		product["sku"], product["quantity"] = 2001, f.quantity.Load()
		delete(product, "product_id")
		delete(product, "vat")
		if f.phase.Load() == 2 {
			row["substatus"] = "posting_in_carriage"
		}
		f.reply(w, map[string]any{"result": row})
	case "/v2/delivery-method/list": // FBS is absent from the rFBS directory.
		f.reply(w, map[string]any{"delivery_methods": []any{}, "has_next": false})
	case "/v1/carriage/get":
		status, updated, actions := "new", "2026-11-07T10:00:00Z", []string{}
		if f.phase.Load() == 2 {
			status, updated, actions = "closed", "2026-11-07T10:01:00Z", []string{"get_waybill"}
		}
		f.reply(w, map[string]any{"carriage_id": 5001, "company_id": json.Number(f.config.Scope.OzonAccount), "delivery_method_id": 4001, "warehouse_id": 3001, "status": status, "created_at": "2026-11-07T10:00:00Z", "updated_at": updated, "departure_date": "2026-11-07", "available_actions": actions, "first_mile_type": "dropoff", "integration_type": "ozon"})
	case "/v2/posting/fbs/act/get-postings":
		memberStatus := "awaiting_deliver"
		if f.cancelledMember.Load() {
			memberStatus = "cancelled"
		}
		f.reply(w, map[string]any{"result": []any{map[string]any{"posting_number": "carriage-http-posting", "status": memberStatus, "products": []any{map[string]any{"sku": 2001, "offer_id": "process-offer", "quantity": f.quantity.Load()}}}}})
	case "/v2/posting/fbs/act/check-status":
		f.reply(w, map[string]any{"result": map[string]any{"status": "ready", "act_type": "ozon_digital", "added_to_act": []string{"carriage-http-posting"}, "removed_from_act": []string{}, "is_partial": false, "has_postings_for_next_carriage": false, "partial_num": 0}})
	case "/v1/carriage/create":
		f.creates.Add(1)
		f.phase.Store(1)
		if f.loseCreate.Load() {
			w.WriteHeader(503)
			return
		}
		f.reply(w, map[string]any{"carriage_id": 5001})
	case "/v1/carriage/approve":
		f.approves.Add(1)
		f.phase.Store(2)
		if f.loseApprove.Load() {
			w.WriteHeader(503)
		}
	default:
		f.carriageReadHTTPFixture.serve(w, r)
	}
}
func (f *carriageLifecycleHTTP) previewLifecycle(t *testing.T, action orders.CarriageAction) orders.CarriagePreview {
	t.Helper()
	body := f.scopeBody()
	body["expected_revision"], body["action"], body["delivery_method_id"] = f.config.Revision, action, "4001"
	if action == orders.CarriageCreate {
		body["all_blr_traceable"] = false
	} else {
		body["carriage_id"] = "5001"
	}
	status, raw := f.call(t, http.MethodPost, "/api/orders/carriage/preview", body, true)
	var p orders.CarriagePreview
	if status != 200 || json.Unmarshal(raw, &p) != nil || len(p.Validation) != 0 {
		t.Fatalf("carriage preview blocked: %d %s", status, raw)
	}
	return p
}
func (f *carriageLifecycleHTTP) approveLifecycle(t *testing.T, p orders.CarriagePreview) orders.CarriageWork {
	t.Helper()
	body := f.scopeBody()
	body["preview_id"], body["idempotency_key"], body["acknowledge"], body["all_ready_consent"], body["physical_readiness_confirmed"] = p.ID, "carriage-"+p.ID, true, true, true
	status, raw := f.call(t, http.MethodPost, "/api/orders/carriage/approve", body, true)
	var work orders.CarriageWork
	if status != 200 && status != 202 {
		t.Fatalf("approve failed: %d %s", status, raw)
	}
	if json.Unmarshal(raw, &work) != nil {
		t.Fatal("invalid work response")
	}
	return work
}
func (f *carriageLifecycleHTTP) runLifecycle(t *testing.T, id string, want orders.State) {
	t.Helper()
	runner, err := orderflow.NewCarriageRunner(f.db, f.factory, orderflow.CarriageRunnerOptions{WorkerID: "carriage-lifecycle", PollInterval: time.Millisecond, AllowScope: func(s orders.Scope) bool { return s == f.config.Scope }})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if ran, err := runner.RunNext(f.ctx); err != nil || !ran {
		t.Fatal("worker did not run", ran, err)
	}
	history, err := f.db.CarriageHistory(f.ctx, f.config.Scope, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range history {
		if w.ID == id {
			if w.State != want {
				t.Fatalf("carriage state=%s code=%s want=%s", w.State, w.Code, want)
			}
			return
		}
	}
	t.Fatal("missing durable carriage operation")
}
func TestOrdersCarriageHTTPCreateApproveAndIndependentReadback(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "lost approval acknowledgement"}[lost], func(t *testing.T) {
			f := newCarriageLifecycleHTTP(t)
			created := f.approveLifecycle(t, f.previewLifecycle(t, orders.CarriageCreate))
			f.runLifecycle(t, created.ID, orders.StateAcknowledged)
			f.runLifecycle(t, created.ID, orders.StateReconciled)
			f.loseApprove.Store(lost)
			approved := f.approveLifecycle(t, f.previewLifecycle(t, orders.CarriageApprove))
			state := orders.StateAcknowledged
			if lost {
				state = orders.StateUnknown
			}
			f.runLifecycle(t, approved.ID, state)
			f.runLifecycle(t, approved.ID, orders.StateReconciled)
			if f.creates.Load() != 1 || f.approves.Load() != 1 {
				t.Fatal("write repeated")
			}
			// Fresh discovery permits another batch after the independently verified one.
			f.approveLifecycle(t, f.previewLifecycle(t, orders.CarriageCreate))
		})
	}
}
func TestOrdersCarriageHTTPStaleCompositionNeverDispatches(t *testing.T) {
	f := newCarriageLifecycleHTTP(t)
	work := f.approveLifecycle(t, f.previewLifecycle(t, orders.CarriageCreate))
	f.quantity.Store(3)
	f.runLifecycle(t, work.ID, orders.StateBlocked)
	if f.creates.Load() != 0 {
		t.Fatal("changed full batch dispatched")
	}
}
func TestOrdersCarriageHTTPUnknownCreationNeverReplays(t *testing.T) {
	f := newCarriageLifecycleHTTP(t)
	f.loseCreate.Store(true)
	work := f.approveLifecycle(t, f.previewLifecycle(t, orders.CarriageCreate))
	f.runLifecycle(t, work.ID, orders.StateUnknown)
	f.runLifecycle(t, work.ID, orders.StateUnknown)
	if f.creates.Load() != 1 || f.approves.Load() != 0 {
		t.Fatal("unknown creation repeated")
	}
}

func TestOrdersCarriageHTTPCancelledMemberCannotBorrowPostingReadiness(t *testing.T) {
	f := newCarriageLifecycleHTTP(t)
	f.phase.Store(1)
	f.cancelledMember.Store(true)
	body := f.scopeBody()
	body["expected_revision"], body["action"], body["delivery_method_id"], body["carriage_id"] = f.config.Revision, "APPROVE", "4001", "5001"
	status, raw := f.call(t, http.MethodPost, "/api/orders/carriage/preview", body, true)
	var p orders.CarriagePreview
	if status != 200 || json.Unmarshal(raw, &p) != nil || len(p.Validation) == 0 {
		t.Fatalf("cancelled carriage member became ready: %d %s", status, raw)
	}
	if f.creates.Load() != 0 || f.approves.Load() != 0 {
		t.Fatal("readiness mismatch wrote provider data")
	}
}
