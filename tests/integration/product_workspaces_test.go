//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

func TestProductDraftPersistsWithRevisionAndExactRetry(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	req := postgres.ProductDraftSave{Scope: scope, Provider: "ozon", Actor: "matching-test", Key: "draft-save", State: json.RawMessage(`{"selected":{},"plans":[],"intents":[]}`)}
	first, err := db.SaveProductImportDraft(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := db.SaveProductImportDraft(ctx, req)
	if err != nil || replay.Revision != first.Revision {
		t.Fatalf("save replay: %v %+v", err, replay)
	}
	req.State = json.RawMessage(`{"selected":{},"note":"changed"}`)
	if _, err = db.SaveProductImportDraft(ctx, req); !errors.Is(err, catalogsync.ErrConflict) {
		t.Fatalf("changed same-key save: %v", err)
	}
	req.Key = "other-tab"
	if _, err = db.SaveProductImportDraft(ctx, req); !errors.Is(err, catalogsync.ErrConflict) {
		t.Fatalf("stale tab replaced draft: %v", err)
	}
	loaded, err := db.ProductImportDraft(ctx, scope, "ozon")
	if err != nil || loaded.Revision != first.Revision || !json.Valid(loaded.State) {
		t.Fatalf("draft lost: %+v %v", loaded, err)
	}
	var mappings int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM product_mappings`).Scan(&mappings); err != nil || mappings != 0 {
		t.Fatal("draft created mapping", err)
	}
}

func TestProductMatchingJobTraversesLargeCatalogueAndKeepsGlobalDuplicates(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	active := false
	now := time.Now().UTC()
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now}
	for i := 0; i < 2105; i++ {
		code := fmt.Sprintf("SKU-%04d", i)
		source.Entities = append(source.Entities, domain.ExternalEntity{Kind: "product", ID: code, Article: code, Archived: &active})
		market.Products = append(market.Products, domain.OzonProduct{ProductID: fmt.Sprintf("p-%04d", i), OfferID: code, Archived: &active})
	}
	source.Entities = append(source.Entities, domain.ExternalEntity{Kind: "product", ID: "last-duplicate", Article: "SKU-0001", Archived: &active})
	market.Products = append(market.Products, domain.OzonProduct{ProductID: "z-last-duplicate", OfferID: " SKU-0002 ", Archived: &active})
	if _, err := db.SaveInventoryImport(ctx, source, "large-match-moy"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "large-match-ozon"); err != nil {
		t.Fatal(err)
	}
	job, err := db.CreateProductMatchingJob(ctx, scope, 0, "matching-test", "large-job")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if _, err = db.RunProductMatchingJob(ctx); err != nil {
			t.Fatal(err)
		}
		job, err = db.ProductMatchingJob(ctx, scope, job.ID, "matching-test", 0)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == "COMPLETED" {
			break
		}
		if job.State == "FAILED" || job.State == "STALE" {
			t.Fatalf("job stopped: %+v", job)
		}
	}
	if job.Processed != 2106 || job.Pages != 22 || job.State != "COMPLETED" || job.Preview == nil {
		t.Fatalf("incomplete job: %+v", job)
	}
	if job.Preview.Items[1].Reason != "duplicate_sku" || job.Preview.Items[2].Reason != "duplicate_offer" {
		t.Fatalf("cross-page duplicates: %+v", job.Preview.Items[:3])
	}
	again, err := db.CreateProductMatchingJob(ctx, scope, 0, "matching-test", "large-job")
	if err != nil || again.ID != job.ID {
		t.Fatal("job replay duplicated work", err)
	}
}

func TestProductMatchingJobStopsOnPolicyChangeAndDoesNotWriteMappings(t *testing.T) {
	db, scope := productMatchingFixture(t)
	ctx := context.Background()
	job, err := db.CreateProductMatchingJob(ctx, scope, 0, "matching-test", "stale-job")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE product_matching_jobs SET revision=99 WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.RunProductMatchingJob(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := db.ProductMatchingJob(ctx, scope, job.ID, "matching-test", 0)
	if err != nil || state.State != "STALE" {
		t.Fatalf("policy change ignored: %+v %v", state, err)
	}
	var mappings int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM product_mappings`).Scan(&mappings); err != nil || mappings != 0 {
		t.Fatal("preview wrote mappings", err)
	}
}
