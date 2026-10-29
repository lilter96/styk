//go:build integration

package integration

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestProviderCoordinationMigrationEmptyRollbackAndUpgrade(t *testing.T) {
	db := catalogSyncDB(t)
	dsn := db.Pool.Config().ConnString()
	catalogSyncMigrate(t, dsn, "down-to", "45")
	var removed bool
	if err := db.Pool.QueryRow(context.Background(), `SELECT to_regclass('provider_call_accounts') IS NULL`).Scan(&removed); err != nil || !removed {
		t.Fatalf("provider rollback %v: %v", removed, err)
	}
	catalogSyncMigrate(t, dsn, "up")
	gate := providerCoordinator(t, db)
	p, err := gate.Begin(context.Background(), providerRequest("migration-upgrade"))
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProviderCoordinationMigrationRetainsAttemptHistory(t *testing.T) {
	db := catalogSyncDB(t)
	r := providerRequest("migration-history")
	providerSend(t, providerCoordinator(t, db), r, 200, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down-to", "45")
	if _, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("rollback discarded provider attempt history")
	}
	var attempts int64
	if err := db.Pool.QueryRow(ctx, `SELECT attempts FROM provider_call_accounts WHERE account_id=$1`, r.Scope.Account).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("guarded rollback changed attempts %d: %v", attempts, err)
	}
}

func TestProviderCoordinationHistoryIsBounded(t *testing.T) {
	db := catalogSyncDB(t)
	gate := providerCoordinator(t, db)
	r := providerRequest("bounded-history")
	providerSend(t, gate, r, 200, nil)
	_, err := db.Pool.Exec(context.Background(), `INSERT INTO provider_call_permits(id,provider,account_id,tenant_id,credential_revision,method_key,operation_key,target_key,is_write,state,created_at,lease_until,started_at,finished_at,response_status) SELECT md5('history-'||n::text),p.provider,p.account_id,p.tenant_id,0,p.method_key,p.operation_key,'',false,'finished',clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour',clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour',200 FROM generate_series(1,1005) n CROSS JOIN LATERAL (SELECT * FROM provider_call_permits WHERE account_id=$1 LIMIT 1) p`, r.Scope.Account)
	if err != nil {
		t.Fatal(err)
	}
	p, err := gate.Begin(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	var attempts int64
	if err = db.Pool.QueryRow(context.Background(), `SELECT count(*),(SELECT attempts FROM provider_call_accounts WHERE account_id=$1) FROM provider_call_permits WHERE account_id=$1`, r.Scope.Account).Scan(&count, &attempts); err != nil || count > 1001 || attempts != 1 {
		t.Fatalf("bounded history %d persistent attempts %d: %v", count, attempts, err)
	}
}
