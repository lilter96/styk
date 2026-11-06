//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

func TestCatalogCreatedLinkRequiresImportedIdentityAndPreservesConflicts(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "verified identity"
		if conflict {
			name = "conflicting manual link"
		}
		t.Run(name, func(t *testing.T) {
			db, w, created := catalogSyncAttributeWork(t)
			completion := catalogSyncAttributeCompletion(t, w, created, `[{"id":1,"values":[{"value":"selected"}]}]`)
			if err := db.CompleteCatalogWork(context.Background(), completion); err != nil {
				t.Fatal(err)
			}
			if worked, err := db.RunCreatedProductLink(context.Background()); err != nil || !worked {
				t.Fatalf("waiting: %v %v", worked, err)
			}
			assertCreatedLink(t, db, w, "WAITING")
			createdLinkImports(t, db, w, conflict)
			if _, err := db.Pool.Exec(context.Background(), `UPDATE catalog_created_links SET checked_at=clock_timestamp()-interval '1 minute'`); err != nil {
				t.Fatal(err)
			}
			if worked, err := db.RunCreatedProductLink(context.Background()); err != nil || !worked {
				t.Fatalf("link: %v %v", worked, err)
			}
			state := "LINKED"
			if conflict {
				state = "CONFLICT"
			}
			assertCreatedLink(t, db, w, state)
			if worked, err := db.RunCreatedProductLink(context.Background()); err != nil || worked {
				t.Fatalf("repeated link: %v %v", worked, err)
			}
		})
	}
}

func assertCreatedLink(t *testing.T, db *postgres.Store, w catalogsync.Work, want string) {
	t.Helper()
	var state string
	if err := db.Pool.QueryRow(context.Background(), `SELECT state FROM catalog_created_links WHERE work_id=$1`, w.ID).Scan(&state); err != nil || state != want {
		t.Fatalf("state %s want %s: %v", state, want, err)
	}
}

func createdLinkImports(t *testing.T, db *postgres.Store, w catalogsync.Work, conflict bool) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	active := false
	moy := domain.InventoryImport{Account: w.Scope.MoyskladAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Entities: []domain.ExternalEntity{
		{Kind: "product", ID: "source-1", Name: "Other", Article: "shared-1", Archived: &active},
		{Kind: "product", ID: "source-2", Name: "Created name", Article: "new-created-offer", Archived: &active},
	}}
	ozon := domain.OzonImport{Account: w.Scope.OzonAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "9001", OfferID: "new-created-offer", Name: "Created name", Archived: &active}}}
	if _, err := db.SaveInventoryImport(ctx, moy, "created-link-moy"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, ozon, "created-link-ozon"); err != nil {
		t.Fatal(err)
	}
	if !conflict {
		return
	}
	req := domain.MappingReviewRequest{Tenant: w.Scope.Tenant, MoyskladAccount: w.Scope.MoyskladAccount, OzonAccount: w.Scope.OzonAccount, ProductID: "9001", TargetKind: "product", TargetID: "source-1", SessionID: "manual-conflict", Evidence: domain.MappingOperatorAttested, Lifetime: time.Minute}
	review, err := db.CreateMappingReview(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmMappingReview(ctx, domain.MappingConfirmationRequest{ReviewID: review.Review.ID, SessionID: req.SessionID, IdempotencyKey: "manual-choice", Acknowledged: true, Explanation: "Operator chose another stock item"}); err != nil {
		t.Fatal(err)
	}
}
