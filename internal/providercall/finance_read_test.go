package providercall

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMonthlyRealizationIsAnExplicitReadOnlyPOST(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path     string
		mutation bool
	}{
		{"/v2/finance/realization", false},
		{"/v2/finance/realization/unknown", true},
		{"/v1/finance/accrual/types", false},
		{"/v1/finance/accrual/by-day", false},
		{"/v1/finance/accrual/postings", false},
		{"/v1/finance/accrual/postings/unknown", true},
		{"/v1/roles", false},
		{"/v2/posting/fbs/act/check-status", false},
		{"/v1/carriage/get", false},
		{"/v2/carriage/delivery/list", false},
		{"/v2/posting/fbs/act/get-postings", false},
		{"/v2/posting/fbs/act/get-postings/unknown", true},
		{"/v1/carriage/create", true},
		{"/v1/carriage/approve", true},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodPost, "https://api-seller.ozon.ru"+tc.path, nil)
			if got := isMutation(request); got != tc.mutation {
				t.Fatalf("mutation=%v, want %v", got, tc.mutation)
			}
		})
	}
}
