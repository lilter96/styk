//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/matching"
)

func TestProductWorkspaceMigrationRoundTripAndAuditGuard(t *testing.T) {
	db, scope := productMatchingFixture(t)
	dsn := db.Pool.Config().ConnString()
	catalogSyncMigrate(t, dsn, "down-to", "69")
	catalogSyncMigrate(t, dsn, "up")
	var count int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM source_entities`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("source identities changed: %d %v", count, err)
	}
	p, err := db.SaveProductMatchPolicy(context.Background(), scope, matching.Policy{ExactSKU: true}, "matching-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := migrationCommand(t, ctx, "-dir", "../../migrations", "postgres", dsn, "down-to", "69")
	if _, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("rollback removed policy audit")
	}
	current, err := db.ProductMatchPolicy(ctx, scope)
	if err != nil || current != p {
		t.Fatalf("rollback changed policy: %+v %v", current, err)
	}
}
