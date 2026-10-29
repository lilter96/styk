//go:build integration

package integration

import (
	"os/exec"
	"testing"

	"github.com/lilter96/styk/internal/postgres"
)

// Older rollback tests exercise a specific migration after removing empty SaaS
// additions; merchant history itself is protected by separate rollback tests.
func rollbackEmptySaaSMigrations(t *testing.T, db *postgres.Store) {
	t.Helper()
	for i := 0; i < 5; i++ {
		cmd := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down")
		if _, err := cmd.CombinedOutput(); err != nil {
			t.Fatal("could not roll back empty SaaS migrations")
		}
	}
}
