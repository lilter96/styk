//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/matching"
	"github.com/lilter96/styk/internal/postgres"
)

func productMatchingBulkFixture(t *testing.T, size int) (*postgres.Store, catalog.Scope) {
	t.Helper()
	db, scope := productMatchingFixture(t)
	active := false
	now := time.Now().UTC()
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now.Add(-time.Second), FinishedAt: now}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now.Add(-time.Second), FinishedAt: now}
	for i := 0; i < size; i++ {
		code := fmt.Sprintf("BULK-%04d", i)
		source.Entities = append(source.Entities, domain.ExternalEntity{Kind: "product", ID: code, Article: code, Archived: &active})
		market.Products = append(market.Products, domain.OzonProduct{ProductID: code, OfferID: code, Archived: &active})
	}
	if _, err := db.SaveInventoryImport(context.Background(), source, "bulk-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(context.Background(), market, "bulk-market"); err != nil {
		t.Fatal(err)
	}
	return db, scope
}
func completedProductMatchingJob(t *testing.T, db *postgres.Store, scope catalog.Scope, criterion matching.Criterion) postgres.ProductMatchingJob {
	t.Helper()
	ctx := context.Background()
	job, err := db.CreateProductMatchingJob(ctx, scope, "matching-test", "bulk-search", criterion)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if _, err = db.RunProductMatchingJob(ctx); err != nil {
			t.Fatal(err)
		}
		job, err = db.ProductMatchingJob(ctx, scope, job.ID, "matching-test", -1)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == "COMPLETED" {
			return job
		}
		if job.State == "STALE" || job.State == "FAILED" {
			t.Fatalf("scan failed: %+v", job)
		}
	}
	t.Fatal("matching did not finish")
	return job
}
func TestProductMatchingBulkApprovesWholeCatalogueWithDurableRecovery(t *testing.T) {
	db, scope := productMatchingBulkFixture(t, 205)
	ctx := context.Background()
	job := completedProductMatchingJob(t, db, scope, matching.ArticleOrCode)
	policy, err := db.ProductMatchPolicy(ctx, scope)
	if err != nil || policy.AutoConfirm || policy.AfterImport || policy.Revision != 0 {
		t.Fatalf("one-off search changed permanent policy: %+v %v", policy, err)
	}
	approval := postgres.ProductMatchingApproval{Scope: scope, JobID: job.ID, Actor: "matching-test", Key: "all-products", Expected: 205, Acknowledge: true}
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); failures <- db.ApplyProductMatchingJob(ctx, approval) }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	changed := approval
	changed.Key = "another-key"
	if err = db.ApplyProductMatchingJob(ctx, changed); !errors.Is(err, catalogsync.ErrConflict) {
		t.Fatalf("different intent replaced approval: %v", err)
	}
	if _, err = db.RunProductMatchingJob(ctx); err != nil {
		t.Fatal(err)
	}
	job, err = db.ProductMatchingJob(ctx, scope, job.ID, "matching-test", -1)
	if err != nil || job.Apply.Confirmed != 20 {
		t.Fatalf("missing committed batch: %+v %v", job.Apply, err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE product_matching_jobs SET apply_state='FAILED' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.RetryProductMatchingJob(ctx, scope, job.ID, "matching-test"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err = db.RunProductMatchingJob(ctx); err != nil {
			t.Fatal(err)
		}
		job, err = db.ProductMatchingJob(ctx, scope, job.ID, "matching-test", -1)
		if err != nil {
			t.Fatal(err)
		}
		if job.Apply.State == "COMPLETED" {
			break
		}
		if job.Apply.State == "FAILED" || job.Apply.State == "STALE" {
			t.Fatalf("application stopped: %+v", job.Apply)
		}
	}
	if job.Apply.State != "COMPLETED" || job.Apply.Confirmed != 205 || job.Apply.Skipped != 0 {
		t.Fatalf("not all products matched: %+v", job.Apply)
	}
	if err = db.ApplyProductMatchingJob(ctx, approval); err != nil {
		t.Fatal(err)
	}
	var mappings, audits int
	if err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM product_mappings),(SELECT count(*) FROM product_mapping_confirmation_audits)`).Scan(&mappings, &audits); err != nil || mappings != 205 || audits != 205 {
		t.Fatalf("lost or duplicated confirmations: %d %d %v", mappings, audits, err)
	}
}
func TestProductMatchingBulkStopsOnChangedImportsAndPreservesPartialSuccess(t *testing.T) {
	db, scope := productMatchingBulkFixture(t, 105)
	ctx := context.Background()
	job := completedProductMatchingJob(t, db, scope, matching.ArticleOrCode)
	req := postgres.ProductMatchingApproval{Scope: scope, JobID: job.ID, Actor: "matching-test", Key: "partial-job", Expected: 105, Acknowledge: true}
	foreign := req
	foreign.Actor = "another-actor"
	if err := db.ApplyProductMatchingJob(ctx, foreign); !errors.Is(err, catalogsync.ErrNotFound) {
		t.Fatalf("foreign approval: %v", err)
	}
	unacknowledged := req
	unacknowledged.Acknowledge = false
	if err := db.ApplyProductMatchingJob(ctx, unacknowledged); !errors.Is(err, catalogsync.ErrInvalid) {
		t.Fatalf("implicit approval: %v", err)
	}
	if err := db.ApplyProductMatchingJob(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RunProductMatchingJob(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.SaveInventoryImport(ctx, domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now.Add(-time.Second), FinishedAt: now}, "changed-bulk-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RunProductMatchingJob(ctx); err != nil {
		t.Fatal(err)
	}
	job, err := db.ProductMatchingJob(ctx, scope, job.ID, "matching-test", -1)
	if err != nil || job.Apply.State != "STALE" || job.Apply.Confirmed != 20 {
		t.Fatalf("lost partial outcome: %+v %v", job.Apply, err)
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM product_mappings`).Scan(&count); err != nil || count != 20 {
		t.Fatalf("stale application wrote extra mappings: %d %v", count, err)
	}
}
func TestProductMatchingBulkRollbackRefusesAppliedEvidence(t *testing.T) {
	db, scope := productMatchingBulkFixture(t, 1)
	ctx := context.Background()
	job := completedProductMatchingJob(t, db, scope, matching.ArticleOrCode)
	if err := db.ApplyProductMatchingJob(ctx, postgres.ProductMatchingApproval{Scope: scope, JobID: job.ID, Actor: "matching-test", Key: "rollback-bulk", Expected: 1, Acknowledge: true}); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/00078_product_matching_bulk.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(ctx, strings.Split(string(migration), "-- +goose Down")[1])
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.Code != "P0001" {
		t.Fatalf("bulk evidence rollback allowed: %v", err)
	}
}
func TestProductMatchingBulkCriterionSeparatesArticlesFromCodes(t *testing.T) {
	for _, criterion := range []matching.Criterion{matching.ArticleOrCode, matching.Article, matching.Code} {
		t.Run(string(criterion), func(t *testing.T) {
			db, scope := productMatchingFixture(t)
			ctx := context.Background()
			active := false
			now := time.Now().UTC()
			targets := []domain.ExternalEntity{{Kind: "product", ID: "article", Article: "A", Archived: &active}}
			for i := 0; i < 4; i++ {
				targets = append(targets, domain.ExternalEntity{Kind: "product", ID: fmt.Sprintf("code-%d", i), Code: "A", Archived: &active})
			}
			if _, err := db.SaveInventoryImport(ctx, domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Entities: targets}, "criterion-targets"); err != nil {
				t.Fatal(err)
			}
			job := completedProductMatchingJob(t, db, scope, criterion)
			if criterion == matching.Article {
				if job.Summary["exact"] != 1 || len(job.Examples) != 1 || job.Examples[0].Candidates[0].ID != "article" {
					t.Fatalf("code matches masked unique article: %+v", job)
				}
			} else if job.Summary["exact"] != 0 || job.Summary["conflict"] != 1 {
				t.Fatalf("global code duplicates accepted: %+v", job.Summary)
			}
		})
	}
}
