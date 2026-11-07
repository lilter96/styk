//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/matching"
	"github.com/lilter96/styk/internal/postgres"
)

func TestProductMatchingStandingRuleWaitsForNewImportAndCanBeRevoked(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	p, err := db.SaveProductMatchPolicy(ctx, scope, matching.Policy{ExactSKU: true, AutoConfirm: true, AfterImport: true}, "matching-test")
	if err != nil {
		t.Fatal(err)
	}
	matchingCycleDue(t, db)
	if worked, err := db.RunProductMatchAutomation(ctx); err != nil || worked {
		t.Fatalf("existing snapshot applied: %v %v", worked, err)
	}
	matchingNextImport(t, db, scope, "next")
	matchingCycleDue(t, db)
	if worked, err := db.RunProductMatchAutomation(ctx); err != nil || !worked {
		t.Fatalf("new snapshot ignored: %v %v", worked, err)
	}
	matchingCycleDue(t, db)
	if worked, err := db.RunProductMatchAutomation(ctx); err != nil || worked {
		t.Fatalf("cycle replayed: %v %v", worked, err)
	}
	var mappings, cycles int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_mappings),(SELECT count(*) FROM product_matching_cycles)`).Scan(&mappings, &cycles); err != nil || mappings != 1 || cycles != 1 {
		t.Fatalf("unsafe cycle %d/%d %v", mappings, cycles, err)
	}
	p.AfterImport = false
	if _, err := db.SaveProductMatchPolicy(ctx, scope, p, "matching-test"); err != nil {
		t.Fatal(err)
	}
	matchingNextImport(t, db, scope, "after-revocation")
	matchingCycleDue(t, db)
	if worked, err := db.RunProductMatchAutomation(ctx); err != nil || worked {
		t.Fatalf("revoked rule ran: %v %v", worked, err)
	}
}

func matchingCycleDue(t *testing.T, db *postgres.Store) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `UPDATE product_matching_policies SET last_checked_at=clock_timestamp()-interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
}

func matchingNextImport(t *testing.T, db *postgres.Store, scope catalog.Scope, fingerprint string) {
	t.Helper()
	active := false
	now := time.Now().UTC()
	v := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "p1", Name: "Чашка синяя", Article: "A", Archived: &active}}}
	if _, err := db.SaveInventoryImport(context.Background(), v, fingerprint); err != nil {
		t.Fatal(err)
	}
}

func TestProductMatchingMissingCompletedBatchCannotAuthorize(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	// A latest import without the completion proof must not reuse an older batch.
	_, err := db.Pool.Exec(ctx, `INSERT INTO source_imports(source,account_id,started_at,finished_at,fingerprint,report_scope) VALUES('ozon',$1,clock_timestamp(),clock_timestamp(),'incomplete-proof','all')`, scope.OzonAccount)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.CreateProductMatchingJob(ctx, scope, "matching-test", "incomplete-search", matching.ArticleOrCode); !errors.Is(err, catalogsync.ErrStale) {
		t.Fatalf("incomplete import accepted: %v", err)
	}
}
