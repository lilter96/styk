package console

import (
	"net/http/httptest"
	"testing"
)

func TestProductPagination(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, query  string
		offset, size int
		valid        bool
	}{
		{"legacy", "", 50, 50, true},
		{"page", "page=3&pageSize=20", 40, 20, true},
		{"max size", "pageSize=100", 50, 100, true},
		{"too big", "pageSize=101", 0, 0, false},
		{"zero", "page=0", 0, 0, false},
		{"overflow", "page=100002", 0, 0, false},
		{"offset bound", "page=100001&pageSize=100", 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			offset, size, err := productPagination(httptest.NewRequest("GET", "/api/products?"+tc.query, nil), 50)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.valid && (offset != tc.offset || size != tc.size) {
				t.Fatalf("got %d/%d", offset, size)
			}
		})
	}
}
