//go:build integration

package integration

import "testing"

func TestOrdersNativeSchemaConstraints(t *testing.T) {
	f := newOrdersStoreFixture(t)
	for _, table := range []string{"orders_native_document_intents", "orders_native_document_targets", "orders_native_document_results"} {
		var count int
		if err := f.db.Pool.QueryRow(f.ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("migration fabricated native history")
		}
	}
	var count int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid='orders_native_document_intents'::regclass AND contype='f'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count < 3 {
		t.Fatal("native intent lacks exact work, parent or return basis foreign keys")
	}
}

func TestOrdersNativeMigrationRoundTripPreservesExistingData(t *testing.T) {
	f := newOrdersStoreFixture(t)
	dsn := f.db.Pool.Config().ConnString()
	catalogSyncMigrate(t, dsn, "down-to", "54")
	var count int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_configs`).Scan(&count); err != nil || count != 1 {
		t.Fatal("native rollback lost existing config", err)
	}
	catalogSyncMigrate(t, dsn, "up")
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_native_document_intents`).Scan(&count); err != nil || count != 0 {
		t.Fatal("native upgrade fabricated history", err)
	}
}
