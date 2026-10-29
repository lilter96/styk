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
