//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/postgres"
)

func TestOrdersStartupRequiresActualCurrentSchema(t *testing.T) {
	db := catalogSyncDB(t)
	ctx := context.Background()
	if err := db.RequireCurrentSchema(ctx); err != nil {
		t.Fatal(err)
	}
	dsn := db.Pool.Config().ConnString()
	catalogSyncMigrate(t, dsn, "down")
	if err := db.RequireCurrentSchema(ctx); !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatal("older migration version looked ready", err)
	}
	catalogSyncMigrate(t, dsn, "up")
	if err := db.RequireCurrentSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE orders_posting_heads RENAME COLUMN projection TO incomplete_projection`); err != nil {
		t.Fatal(err)
	}
	if err := db.RequireCurrentSchema(ctx); !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatal("schema version alone concealed missing required structure", err)
	}
}

func TestOrdersStartupRequiresCompositionActionConstraint(t *testing.T) {
	db := catalogSyncDB(t)
	ctx := context.Background()
	if err := db.RequireCurrentSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE orders_work DROP CONSTRAINT orders_work_action_check; ALTER TABLE orders_work ADD CONSTRAINT orders_work_action_check CHECK(action IN ('mirror_create','posting_country_set'))`); err != nil {
		t.Fatal(err)
	}
	if err := db.RequireCurrentSchema(ctx); !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatal("reported current version concealed missing composition action", err)
	}
}

func TestOrdersStartupRequiresNativePointStructures(t *testing.T) {
	for _, statement := range []string{
		`ALTER TABLE orders_ingestion_leases RENAME COLUMN attempt_started_at TO missing_attempt_clock`,
		`ALTER TABLE orders_native_point_reads RENAME TO missing_native_reads`,
		`ALTER TABLE orders_native_point_snapshots RENAME TO missing_native_snapshots`,
		`ALTER TABLE orders_native_document_reads RENAME TO missing_document_reads`,
		`ALTER TABLE orders_native_document_snapshots RENAME TO missing_document_snapshots`,
		`ALTER TABLE orders_native_document_events RENAME TO missing_native_events`,
		`ALTER TABLE orders_native_document_retries RENAME TO missing_native_retries`,
	} {
		t.Run(statement, func(t *testing.T) {
			db := catalogSyncDB(t)
			ctx := context.Background()
			if err := db.RequireCurrentSchema(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, statement); err != nil {
				t.Fatal(err)
			}
			if err := db.RequireCurrentSchema(ctx); !errors.Is(err, postgres.ErrSchemaIncompatible) {
				t.Fatal("migration version concealed missing native point structure", err)
			}
		})
	}
}
