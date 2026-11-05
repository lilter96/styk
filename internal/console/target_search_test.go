package console

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTargetSearchOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, query  string
		valid        bool
		offset, size int
	}{
		{"defaults", "", true, 0, 50},
		{"page", "page=3&pageSize=20&searchBy=name&query=shirt&inStockOnly=true", true, 40, 20},
		{"oversized", "pageSize=101", false, 0, 0},
		{"negative", "page=-1", false, 0, 0},
		{"overflow", "page=100001&pageSize=100", false, 0, 0},
		{"field injection", "searchBy=name%3BDROP", false, 0, 0},
		{"invalid bool", "activeOnly=maybe", false, 0, 0},
		{"long query", "query=" + strings.Repeat("x", 101), false, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := targetOptions(httptest.NewRequest("GET", "/api/moysklad-products?"+tc.query, nil), 0)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && (result.Offset != tc.offset || result.PageSize != tc.size) {
				t.Fatalf("unexpected page: %+v", result)
			}
		})
	}
}
