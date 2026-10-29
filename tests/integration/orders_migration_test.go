//go:build integration

package integration

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

func TestOrdersStoreMigrationCleanRollbackAndUpgrade(t *testing.T) {
	db := catalogSyncDB(t)
	dsn := db.Pool.Config().ConnString()
	catalogSyncMigrate(t, dsn, "down-to", "44")
	if _, err := db.Submit(context.Background(), domain.Observation{OperationKey: "orders-upgrade-legacy", Source: "test", SKU: "legacy", Quantity: 1}, 3); err != nil {
		t.Fatal(err)
	}
	catalogSyncMigrate(t, dsn, "up")
	task, err := db.Claim(context.Background(), time.Minute)
	if err != nil || task == nil || task.OperationKey != "orders-upgrade-legacy" {
		t.Fatalf("legacy task changed during orders upgrade: %+v %v", task, err)
	}
	if err = db.Finish(context.Background(), *task, "preserved", nil, 0); err != nil {
		t.Fatal(err)
	}
}

func TestOrdersStoreMigrationRetainsHistory(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", fixture.db.Pool.Config().ConnString(), "down-to", "44")
	if _, err := command.CombinedOutput(); err == nil {
		t.Fatal("rollback discarded immutable order configuration history")
	}
	config, err := fixture.db.OrderConfig(fixture.ctx, fixture.config.Scope)
	if err != nil || config.Revision != fixture.config.Revision {
		t.Fatalf("guarded rollback altered order evidence: %+v %v", config, err)
	}
}
