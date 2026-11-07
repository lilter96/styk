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

func TestProductMatchingSearchIsReadOnlyAndOneOff(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	job, err := db.CreateProductMatchingJob(ctx, scope, "matching-test", "read-only", matching.ArticleOrCode)
	if err != nil || job.Summary["exact"] != 1 || job.Summary["conflict"] != 1 || job.Summary["unmatched"] != 1 {
		t.Fatalf("search: %+v %v", job, err)
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM product_mappings`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("search wrote mappings: %d %v", count, err)
	}
	foreign := scope
	foreign.OzonAccount = "foreign"
	if _, err = db.CreateProductMatchingJob(ctx, foreign, "matching-test", "foreign", matching.ArticleOrCode); !errors.Is(err, catalogsync.ErrNotFound) {
		t.Fatalf("foreign search: %v", err)
	}
	app, err := db.ApplyProductMatchingJob(ctx, postgres.ProductMatchingApproval{Scope: scope, JobID: job.ID, Actor: "matching-test", Key: "one-off", Expected: 1, Acknowledge: true})
	if err != nil || app.State != "COMPLETED" || app.Confirmed != 1 {
		t.Fatalf("one-off required a permanent rule: %+v %v", app, err)
	}
	policy, err := db.ProductMatchPolicy(ctx, scope)
	if err != nil || policy.AutoConfirm || policy.AfterImport || policy.Revision != 0 {
		t.Fatalf("one-off changed policy: %+v %v", policy, err)
	}
}

func TestProductMatchingConcurrentReplayPreservesExceptions(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	job, err := db.CreateProductMatchingJob(ctx, scope, "matching-test", "concurrent-search", matching.ArticleOrCode)
	if err != nil {
		t.Fatal(err)
	}
	req := postgres.ProductMatchingApproval{Scope: scope, JobID: job.ID, Actor: "matching-test", Key: "concurrent-approval", Expected: 1, Acknowledge: true}
	var wg sync.WaitGroup
	results := make([]postgres.ProductMatchingApplication, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results[i], errs[i] = db.ApplyProductMatchingJob(ctx, req) }()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].Confirmed != 1 || results[1].Confirmed != 1 {
		t.Fatalf("replay: %+v %v", results, errs)
	}
	var maps, audits, outcomes int
	if err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_mappings),(SELECT count(*) FROM product_mapping_confirmation_audits),(SELECT count(*) FROM product_matching_job_results)`).Scan(&maps, &audits, &outcomes); err != nil || maps != 1 || audits != 1 || outcomes != 1 {
		t.Fatalf("atomic exact-only outcome: %d/%d/%d %v", maps, audits, outcomes, err)
	}
}

func TestProductMatchingManualConfirmationIsPreservedAtApply(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	job, err := db.CreateProductMatchingJob(ctx, scope, "matching-test", "before-manual", matching.ArticleOrCode)
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
	app, err := db.ApplyProductMatchingJob(ctx, postgres.ProductMatchingApproval{Scope: scope, JobID: job.ID, Actor: "matching-test", Key: "after-manual", Expected: 1, Acknowledge: true})
	if err != nil || app.Confirmed != 0 || app.Skipped != 1 || app.State != "COMPLETED" {
		t.Fatalf("manual confirmation was replaced: %+v %v", app, err)
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM product_mapping_confirmation_audits`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate audit: %d %v", count, err)
	}
}
