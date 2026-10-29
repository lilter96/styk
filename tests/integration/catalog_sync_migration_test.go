//go:build integration

package integration

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

func TestCatalogSyncMigrationCleanRollbackAndUpgrade(t *testing.T) {
	db := catalogSyncDB(t)
	dsn := db.Pool.Config().ConnString()
	catalogSyncMigrate(t, dsn, "down-to", "42")
	if _, err := db.Submit(context.Background(), domain.Observation{OperationKey: "pre-catalog-task", Source: "test", SKU: "upgrade", Quantity: 1}, 3); err != nil {
		t.Fatal(err)
	}
	catalogSyncMigrate(t, dsn, "up")
	task, err := db.Claim(context.Background(), time.Minute)
	if err != nil || task == nil || task.OperationKey != "pre-catalog-task" {
		t.Fatalf("legacy task changed during upgrade: %+v %v", task, err)
	}
	if err = db.Finish(context.Background(), *task, "legacy survived upgrade", nil, 0); err != nil {
		t.Fatal(err)
	}
	var table bool
	if err = db.Pool.QueryRow(context.Background(), `SELECT to_regclass('catalog_sync_work') IS NOT NULL`).Scan(&table); err != nil || !table {
		t.Fatalf("catalog schema missing: %v %v", table, err)
	}
}

func TestCatalogSyncMigrationRefusesEvidenceLoss(t *testing.T) {
	db, c := catalogSyncFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down-to", "42")
	if _, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("rollback discarded catalog audit")
	}
	current, err := db.CatalogConfig(ctx, c.Scope)
	if err != nil || current.Revision != c.Revision {
		t.Fatalf("guarded rollback changed catalog evidence: %+v %v", current, err)
	}
}
