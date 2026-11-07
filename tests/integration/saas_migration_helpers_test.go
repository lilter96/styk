//go:build integration

package integration

import (
	"context"
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
		cmd := migrationCommand(t, context.Background(), "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down")
		if _, err := cmd.CombinedOutput(); err != nil {
			t.Fatal("could not roll back empty SaaS migrations")
		}
	}
}
