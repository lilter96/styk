//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/matching"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/stockfixture"
)

func productMatchingFixture(t *testing.T) (*postgres.Store, catalog.Scope) {
	t.Helper()
	db := catalogSyncDB(t)
	s := stockfixture.Scope()
	scope := catalog.Scope{Tenant: s.Tenant, MoyskladAccount: s.MoyskladAccount, OzonAccount: s.OzonAccount}
	catalogSyncConnections(t, db, scope)
	active := false
	now := time.Now().UTC()
	source := domain.InventoryImport{Account: s.MoyskladAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Entities: []domain.ExternalEntity{
		{Kind: "product", ID: "p1", Name: "Чашка синяя", Article: "A", Archived: &active},
		{Kind: "product", ID: "p2", Name: "Чашка красная", Article: "B", Archived: &active},
		{Kind: "product", ID: "p3", Name: "Чашка красная", Article: "B", Archived: &active},
	}}
	market := domain.OzonImport{Account: s.OzonAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Products: []domain.OzonProduct{
		{ProductID: "101", OfferID: "A", Name: "Чашка синяя", Archived: &active},
		{ProductID: "102", OfferID: "B", Name: "Чашка красная", Archived: &active},
		{ProductID: "103", OfferID: "C", Name: "Чашка синяя", Archived: &active},
	}}
	if _, err := db.SaveInventoryImport(context.Background(), source, "matching-moy-v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(context.Background(), market, "matching-ozon-v1"); err != nil {
		t.Fatal(err)
	}
	return db, scope
}

func TestProductMatchingPreviewIsReadOnlyAndOptIn(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	p, err := db.PreviewProductMatches(ctx, scope, 0, "matching-test")
	if err != nil || len(p.Items) != 3 {
		t.Fatalf("preview: %+v %v", p, err)
	}
	if p.Items[0].Status != "exact" || p.Items[1].Status != "conflict" || p.Items[2].Status != "review" {
		t.Fatalf("classification: %+v", p.Items)
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM product_mappings`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("preview wrote: %d %v", count, err)
	}
	req := postgres.ProductMatchExecute{Scope: scope, PreviewID: p.ID, Actor: "matching-test", IdempotencyKey: "only-opt-in", Products: []string{"101"}, Acknowledge: true}
	if _, err = db.ExecuteProductMatches(ctx, req); !errors.Is(err, catalogsync.ErrBlocked) {
		t.Fatalf("default allowed write: %v", err)
	}
	foreign := scope
	foreign.OzonAccount = "foreign"
	if _, err = db.PreviewProductMatches(ctx, foreign, 0, "matching-test"); !errors.Is(err, catalogsync.ErrNotFound) {
		t.Fatalf("foreign scope: %v", err)
	}
}

func TestProductMatchingConcurrentReplayAndPartialResults(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	policy, err := db.SaveProductMatchPolicy(ctx, scope, matching.Policy{ExactSKU: true, Names: true, AutoConfirm: true}, "matching-test")
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.PreviewProductMatches(ctx, scope, policy.Revision, "matching-test")
	if err != nil {
		t.Fatal(err)
	}
	req := postgres.ProductMatchExecute{Scope: scope, PreviewID: p.ID, Actor: "matching-test", IdempotencyKey: "concurrent-run", Products: []string{"101", "102", "103"}, Acknowledge: true}
	var wg sync.WaitGroup
	results := make([]postgres.ProductMatchRun, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results[i], errs[i] = db.ExecuteProductMatches(ctx, req) }()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].ID != results[1].ID || results[0].Confirmed != 1 {
		t.Fatalf("replay: %+v %v", results, errs)
	}
	if results[0].Items[1].Status != "conflict" || results[0].Items[2].Status != "review" {
		t.Fatal("unsafe items confirmed")
	}
	var maps, audits, runs int
	if err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_mappings),(SELECT count(*) FROM product_mapping_confirmation_audits),(SELECT count(*) FROM product_matching_runs)`).Scan(&maps, &audits, &runs); err != nil || maps != 1 || audits != 1 || runs != 1 {
		t.Fatalf("atomic audit %d/%d/%d %v", maps, audits, runs, err)
	}
	req.Products = []string{"103"}
	if _, err = db.ExecuteProductMatches(ctx, req); !errors.Is(err, catalogsync.ErrConflict) {
		t.Fatalf("key reuse accepted: %v", err)
	}
}

func TestProductMatchingManualConfirmationInvalidatesPlan(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	policy, err := db.SaveProductMatchPolicy(ctx, scope, matching.Policy{ExactSKU: true, AutoConfirm: true}, "matching-test")
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.PreviewProductMatches(ctx, scope, policy.Revision, "matching-test")
	if err != nil {
		t.Fatal(err)
	}
	review, err := db.CreateMappingReview(ctx, domain.MappingReviewRequest{Tenant: scope.Tenant, OzonAccount: scope.OzonAccount, MoyskladAccount: scope.MoyskladAccount, ProductID: "101", TargetKind: "product", TargetID: "p1", SessionID: "matching-test", Evidence: domain.MappingOperatorAttested, Lifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmMappingReview(ctx, domain.MappingConfirmationRequest{ReviewID: review.Review.ID, SessionID: "matching-test", IdempotencyKey: "manual", Acknowledged: true}); err != nil {
		t.Fatal(err)
	}
	req := postgres.ProductMatchExecute{Scope: scope, PreviewID: p.ID, Actor: "matching-test", IdempotencyKey: "stale-run", Products: []string{"101"}, Acknowledge: true}
	if _, err = db.ExecuteProductMatches(ctx, req); !errors.Is(err, catalogsync.ErrStale) {
		t.Fatalf("manual change not rechecked: %v", err)
	}
}
