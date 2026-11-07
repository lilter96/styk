//go:build integration

package postgres

import (
	"strings"
	"testing"
)

func TestInventoryDatabaseGuardAcceptsOnlyDedicatedLoopbackOwnedWorkers(t *testing.T) {
	t.Parallel()
	const path = "/styk_full_123_456789_7_test"
	tests := []struct {
		name, raw string
		allowed   bool
	}{
		{"owned worker", "postgres://synthetic@127.0.0.1:5432" + path + "?sslmode=disable", true},
		{"legacy dedicated fixture", "postgres://localhost/styk_orders_store_test?sslmode=disable", true},
		{"unrelated production", "postgres://localhost/production", false},
		{"wrong worker index", "postgres://localhost/styk_full_123_456_8_test", false},
		{"missing nonce", "postgres://localhost/styk_full_123__3_test", false},
		{"long truncated lookalike", "postgres://localhost/styk_full_" + strings.Repeat("1", 70) + "_456_0_test", false},
		{"remote URL host", "postgres://remote.example" + path, false},
		{"database query override", "postgres://localhost" + path + "?dbname=production", false},
		{"effective remote host", "postgres://localhost" + path + "?host=remote.example", false},
		{"remote fallback", "postgres://localhost" + path + "?host=localhost,remote.example", false},
		{"unix socket override", "postgres://localhost" + path + "?host=/tmp", false},
		{"malformed query", "postgres://localhost" + path + "?sslmode=disable;host=remote.example", false},
		{"fragment", "postgres://localhost" + path + "#production", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := inventoryTestDatabaseURL(tc.raw)
			if (err == nil) != tc.allowed {
				t.Fatal("unsafe fixture scope decision")
			}
		})
	}
}
