//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersAppliedSplitNonSuccessKeepsUnknownFenceWithoutReplay(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			f, provider, before := unmirroredSplitFixture(t)
			f.provider.base.server.Config.Handler = appliedSplitFailureHandler(t, provider, status)
			unit := orders.ActionQuantity{Identity: before.Lines[0].Identity, Quantity: "1"}
			proposal := orders.ActionProposal{PostingNumber: before.PostingNumber, Action: orders.ActionPostingSplit, Parameters: orders.ActionParameters{ReadinessConfirmed: true, Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{unit}}, {Lines: []orders.ActionQuantity{unit}}}}}
			f.approve(t, splitMemberHTTPPreview(t, f, before.PostingNumber, []orders.ActionProposal{proposal}))
			f.run(t, orders.StateUnknown)
			work := f.latestWork(t)
			if work.DispatchCount != 1 || work.Receipt.HTTPStatus != status || work.Receipt.State != orders.StateUnknown || work.Code != "PROVIDER_UNCERTAIN" || provider.writes.Load() != 1 || !provider.split.Load() {
				t.Fatalf("applied split released its fence: state=%s code=%s HTTP=%d dispatch=%d", work.State, work.Code, work.Receipt.HTTPStatus, work.DispatchCount)
			}
			f.runner = f.restartedRunner(t)
			f.run(t, orders.StateUnknown)
			if provider.writes.Load() != 1 || f.latestWork(t).DispatchCount != 1 {
				t.Fatal("unknown split was replayed after worker restart")
			}
			assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_target_fences`, 1)
			assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_write_intents`, 1)
		})
	}
}

func appliedSplitFailureHandler(t *testing.T, provider *unmirroredSplitHTTPProvider, status int) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/posting/fbs/ship" {
			provider.serve(w, r)
			return
		}
		recorded := httptest.NewRecorder()
		provider.serve(recorded, r)
		if recorded.Code != http.StatusOK {
			t.Error("synthetic split did not apply before failure")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(status)
		fixtureText(t, w, `{"code":9,"message":"PRIVATE_PROVIDER_ERROR"}`)
	})
}
