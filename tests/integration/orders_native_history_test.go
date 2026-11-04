//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func (f nativeMovementHTTPFixture) assertVerifiedNativeHistory(t *testing.T) {
	t.Helper()
	scope := f.config.Scope
	query := url.Values{"tenant": {scope.Tenant}, "moysklad_account": {scope.MoyskladAccount}, "ozon_account": {scope.OzonAccount}, "limit": {"25"}}
	status, raw := f.merchant.request(t, http.MethodGet, "/api/orders/operations?"+query.Encode(), "", false)
	var history orders.HistoryPage
	if status != 200 || json.Unmarshal(raw, &history) != nil {
		t.Fatalf("native history HTTP %d", status)
	}
	workID := f.latestWork(t).ID
	for _, operation := range history.Operations {
		for _, work := range operation.Targets {
			if work.ID != workID {
				continue
			}
			if work.NativeDocument == nil || work.NativeDocument.ID == "" || work.NativeDocument.Kind != "demand" || work.NativeDocument.ObservedAt.IsZero() {
				t.Fatal("verified native reference missing after lost acknowledgment")
			}
			if work.Receipt.HTTPStatus != 0 || work.Receipt.State != orders.StateUnknown || work.Receipt.ExternalID != "" {
				t.Fatal("native readback fabricated the original external receipt")
			}
			return
		}
	}
	t.Fatal("native history work absent")
}
