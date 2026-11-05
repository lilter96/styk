//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

// Official examples contain error:[] despite their shared schema saying string.
// Incomplete/malformed target results remain UNKNOWN rather than releasing the
// durable fence. Every case uses the merchant API and ordinary scoped worker.
func TestOrdersCuratedMoveAcknowledgementsPreserveExactOutcome(t *testing.T) {
	for _, tc := range []struct {
		id    string
		state orders.State
	}{
		{"rfbs-delivering-ack", orders.StateAcknowledged},
		{"move-missing-result", orders.StateUnknown},
		{"move-null-error-element", orders.StateUnknown},
		{"move-contradictory-result", orders.StateUnknown},
		{"move-explicit-reject", orders.StateRejected},
	} {
		t.Run(tc.id, func(t *testing.T) {
			f, p := newExpressExecutionHTTPFixture(t)
			_, raw, err := ordercontract.CuratedFulfillmentFixture(tc.id)
			if err != nil {
				t.Fatal(err)
			}
			raw = []byte(strings.ReplaceAll(string(raw), "STYK-CURATED-0001-1", "position-http-posting"))
			var calls atomic.Int32
			p.base.base.server.Config.Handler = curatedMoveHTTPHandler(t, p, raw, &calls, tc.state == orders.StateAcknowledged)
			preview := expressExecutionHTTPPreview(t, f)
			if !preview.Decisions[0].Ready {
				t.Fatal("curated profile not ready", preview.Decisions[0].Validation)
			}
			operation := f.approve(t, preview)
			exemplarHTTPRun(t, f.ordersStoreFixture, f.runner, operation.ID, tc.state)
			assertCuratedMoveOutcome(t, f, p, &calls, operation.ID, tc.state)
		})
	}
}

func curatedMoveHTTPHandler(t *testing.T, p *expressExecutionHTTPProvider, raw []byte, calls *atomic.Int32, applied bool) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/fbs/posting/delivering" {
			p.serve(w, r)
			return
		}
		if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+r.Header.Get("Client-Id") {
			w.WriteHeader(403)
			return
		}
		var body struct {
			PostingNumbers []string `json:"posting_number"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || len(body.PostingNumbers) != 1 || body.PostingNumbers[0] != "position-http-posting" {
			w.WriteHeader(400)
			return
		}
		calls.Add(1)
		p.applied.Store(applied)
		w.Header().Set("Content-Type", "application/json")
		fixtureText(t, w, string(raw))
	})
}

func assertCuratedMoveOutcome(t *testing.T, f positionHTTPFixture, p *expressExecutionHTTPProvider, calls *atomic.Int32, id string, state orders.State) {
	t.Helper()
	work := f.latestWork(t)
	if work.Receipt.HTTPStatus != 200 || work.DispatchCount != 1 || calls.Load() != 1 {
		t.Fatal("target acknowledgement lost dispatch evidence")
	}
	if strings.Contains(work.Receipt.Code, "CUSTOMER") || strings.Contains(work.Receipt.Code, "@") {
		t.Fatal("provider free text leaked into receipt")
	}
	if state == orders.StateAcknowledged {
		exemplarHTTPRun(t, f.ordersStoreFixture, f.runner, id, orders.StateReconciled)
		return
	}
	if state == orders.StateUnknown {
		assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_target_fences`, 1)
		f.runner = f.restartedRunner(t)
		exemplarHTTPRun(t, f.ordersStoreFixture, f.runner, id, orders.StateUnknown)
		if calls.Load() != 1 || p.applied.Load() {
			t.Fatal("unknown response replayed an external write")
		}
	}
}
