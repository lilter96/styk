//go:build integration

package integration

import (
	"context"
	"os/exec"
	"testing"

	"github.com/lilter96/styk/internal/postgres"
)

// Older rollback tests exercise a specific migration after removing empty SaaS
// additions; merchant history itself is protected by separate rollback tests.
func rollbackEmptySaaSMigrations(t *testing.T, db *postgres.Store) {
	t.Helper()
	for {
		var version int64
		if err := db.Pool.QueryRow(context.Background(), `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version <= 32 {
			break
		}
		cmd := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down")
		if _, err := cmd.CombinedOutput(); err != nil {
			t.Fatal("could not roll back empty SaaS migrations")
		}
	}
}
