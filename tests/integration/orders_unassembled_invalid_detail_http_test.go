//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

// A valid detail from another commercial order must not discard nine good
// reads, or keep the original posting outside the worker's bounded frontier.
func TestOrdersUnassembledSplitHTTPWrongOrderDetailPreservesProgress(t *testing.T) {
	f, provider, preview := unassembledHTTPFixture(t)
	var corrupt atomic.Bool
	corrupt.Store(true)
	f.provider.base.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		provider.serve(recorder, r)
		forwardUnassembledDetail(t, w, r, recorder, corrupt.Load())
	})
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StatePolling)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_split_readbacks`, 10)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_posting_observations WHERE commercial_order_id='999999'`, 0)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_split_readbacks WHERE read_code='SOURCE_UNAVAILABLE' AND observation_id IS NULL`, 1)
	f.runner = f.restartedRunner(t)
	f.run(t, orders.StatePolling)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_split_readbacks`, 11)
	corrupt.Store(false)
	for range 3 {
		if f.latestWork(t).State == orders.StateReconciled {
			break
		}
		f.run(t, orders.StateReconciled)
	}
	if f.latestWork(t).State != orders.StateReconciled || provider.writes.Load() != 1 {
		t.Fatal("corrected detail did not reconcile exactly one original dispatch")
	}
}

func forwardUnassembledDetail(t *testing.T, w http.ResponseWriter, r *http.Request, recorder *httptest.ResponseRecorder, corrupt bool) {
	t.Helper()
	var payload map[string]map[string]any
	if r.URL.Path == "/v3/posting/fbs/get" && recorder.Code == http.StatusOK && corrupt {
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if payload["result"]["posting_number"] == ordersProcessNumber("unassembled-child", 0) {
			payload["result"]["order_id"] = "999999"
			if err := json.NewEncoder(w).Encode(payload); err != nil {
				t.Error(err)
			}
			return
		}
	}
	w.WriteHeader(recorder.Code)
	if _, err := w.Write(recorder.Body.Bytes()); err != nil {
		t.Error(err)
	}
}
