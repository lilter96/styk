package console

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFinancePreviewRejectsAmbiguousPeriodBeforeServiceRead(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"year=2026", "year=bad&month=9", "year=2026&month=9&month=10", "year=2025&year=2026&month=9"} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "/api/orders/finance/realization?"+query, nil)
			response := httptest.NewRecorder()
			(&ordersAPI{}).realizationReport(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("ambiguous period reached provider: %d", response.Code)
			}
		})
	}
}
