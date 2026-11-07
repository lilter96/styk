package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCommissionApprovalRejectsMissingConsentAndClientAutomation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body string }{
		{name: "missing explicit consent", body: `{"tenant":"t","moysklad_account":"m","ozon_account":"o","preview_id":"preview","idempotency_key":"key","acknowledge":false}`},
		{name: "cannot inject automatic authority", body: `{"tenant":"t","moysklad_account":"m","ozon_account":"o","preview_id":"preview","idempotency_key":"key","acknowledge":true,"automatic":true}`},
		{name: "cannot inject actor", body: `{"tenant":"t","moysklad_account":"m","ozon_account":"o","preview_id":"preview","idempotency_key":"key","acknowledge":true,"actor":"FINANCE:forged:1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodPost, "/api/orders/finance/approve", strings.NewReader(tc.body))
			response := httptest.NewRecorder()
			(&ordersAPI{}).commissionApprove(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatal("untrusted authority reached finance service", response.Code)
			}
		})
	}
}
