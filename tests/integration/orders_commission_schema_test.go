//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/postgres"
)

func TestOrdersCommissionSchemaGateRequiresVersionAndScopedLedger(t *testing.T) {
	db, _ := ordersProcessDB(t)
	ctx := context.Background()
	if err := db.RequireCurrentSchema(ctx); err != nil {
		t.Fatal("fully migrated finance schema rejected", err)
	}
	for _, change := range []struct{ breakSQL, repairSQL string }{
		{`UPDATE goose_db_version SET is_applied=false WHERE version_id=73`, `UPDATE goose_db_version SET is_applied=true WHERE version_id=73`},
		{`UPDATE goose_db_version SET is_applied=false WHERE version_id=74`, `UPDATE goose_db_version SET is_applied=true WHERE version_id=74`},
		{`UPDATE goose_db_version SET is_applied=false WHERE version_id=75`, `UPDATE goose_db_version SET is_applied=true WHERE version_id=75`},
		{`UPDATE goose_db_version SET is_applied=false WHERE version_id=76`, `UPDATE goose_db_version SET is_applied=true WHERE version_id=76`},
		{`ALTER TABLE orders_finance_work RENAME TO missing_finance_work`, `ALTER TABLE missing_finance_work RENAME TO orders_finance_work`},
		{`ALTER TABLE orders_finance_raw_evidence RENAME TO missing_finance_raw_evidence`, `ALTER TABLE missing_finance_raw_evidence RENAME TO orders_finance_raw_evidence`},
		{`ALTER TABLE orders_finance_ledgers RENAME TO missing_finance_ledgers`, `ALTER TABLE missing_finance_ledgers RENAME TO orders_finance_ledgers`},
		{`ALTER TABLE orders_carriage_heads RENAME TO missing_carriage_heads`, `ALTER TABLE missing_carriage_heads RENAME TO orders_carriage_heads`},
	} {
		if _, err := db.Pool.Exec(ctx, change.breakSQL); err != nil {
			t.Fatal(err)
		}
		if err := db.RequireCurrentSchema(ctx); !errors.Is(err, postgres.ErrSchemaIncompatible) {
			t.Fatal("incomplete finance upgrade passed startup gate", err)
		}
		if _, err := db.Pool.Exec(ctx, change.repairSQL); err != nil {
			t.Fatal(err)
		}
		if err := db.RequireCurrentSchema(ctx); err != nil {
			t.Fatal("restored finance schema remained unavailable", err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE outbox_tasks DROP CONSTRAINT finance_outbox_scope_fk`); err != nil {
		t.Fatal(err)
	}
	if err := db.RequireCurrentSchema(ctx); !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatal("unscoped finance outbox passed startup gate", err)
	}
}
