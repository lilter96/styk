//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/domain"
)

func TestDatabaseFixturesRetainIndependentQueuesAndSequences(t *testing.T) {
	t.Parallel()
	a, b := catalogSyncDB(t), catalogSyncDB(t)
	ctx := context.Background()
	first, err := a.Submit(ctx, domain.Observation{OperationKey: "isolated-identical-key", Source: "test", SKU: "isolation", Quantity: 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = b.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_tasks`).Scan(&count); err != nil || count != 0 {
		t.Fatal("fixture queues share mutable rows", count, err)
	}
	second, err := b.Submit(ctx, domain.Observation{OperationKey: "isolated-identical-key", Source: "test", SKU: "isolation", Quantity: 1}, 1)
	if err != nil {
		t.Fatal("same operation key leaked between fixtures", err)
	}
	if first != second {
		t.Fatal("empty fixture sequences were not independent")
	}
	if err = a.RequireCurrentSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = b.RequireCurrentSchema(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestOrdersProcessDatabaseNamesRejectUnrelatedDatabases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		allowed bool
	}{
		{"/styk_orders_store_test", true}, {"/styk_orders_clone_123_456", true},
		{"/styk_full_123_456789_0_test", true}, {"/styk_full_123_456789_7_test", true},
		{"/styk_full_123_456789_8_test", false}, {"/styk_full_123_456789_00_test", false},
		{"/styk_full_0_456789_0_test", false}, {"/styk_full_123__0_test", false},
		{"/styk_full_123_nonce_0_test", false}, {"/styk_full_123_456789_0_test_prod", false},
		{"/production_styk_full_123_456789_0_test", false},
		{"/styk_full_" + strings.Repeat("1", 70) + "_456_0_test", false},
		{"/production", false}, {"/styk_orders_clone_production", false}, {"/styk_orders_clone_123_456_suffix", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ordersProcessDatabaseName(tc.name) != tc.allowed {
				t.Fatal("invalid isolated process database classification")
			}
		})
	}
}

func TestOwnedFullDatabaseURLPreservesEffectiveLoopbackScope(t *testing.T) {
	t.Parallel()
	const path = "/styk_full_123_456789_3_test"
	tests := []struct {
		name, raw string
		allowed   bool
	}{
		{"owned worker", "postgres://synthetic@127.0.0.1:5432" + path + "?sslmode=disable", true},
		{"localhost worker", "postgres://synthetic@localhost" + path, true},
		{"unrelated production", "postgres://localhost/production", false},
		{"name only in userinfo", "postgres://styk_full_123_456789_3_test@localhost/production", false},
		{"remote URL host", "postgres://remote.example" + path, false},
		{"database override", "postgres://localhost" + path + "?dbname=production", false},
		{"effective remote host", "postgres://localhost" + path + "?host=remote.example", false},
		{"remote fallback", "postgres://localhost" + path + "?host=localhost,remote.example", false},
		{"unix socket override", "postgres://localhost" + path + "?host=/tmp", false},
		{"fragment", "postgres://localhost" + path + "#production", false},
		{"malformed query", "postgres://localhost" + path + "?sslmode=disable;host=remote.example", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if ordersProcessDatabaseURL(tc.raw) != tc.allowed {
				t.Fatal("effective database scope guard changed")
			}
		})
	}
}
