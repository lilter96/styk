//go:build integration

package integration

import (
	"context"
	"fmt"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"os"
	"testing"
	"time"
)

func TestPostingsPersistence(t *testing.T) {
	ctx := context.Background()
	db, e := postgres.Open(ctx, os.Getenv("TEST_DATABASE_URL"), 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Pool.Close()
	tenant := fmt.Sprint(time.Now().UnixNano())
	defer func() {
		if _, err := db.Pool.Exec(ctx, `DELETE FROM ozon_posting_identities WHERE tenant_id=$1`, tenant); err != nil {
			t.Errorf("remove fixture posting identities: %v", err)
		}
	}()
	defer func() {
		if _, err := db.Pool.Exec(ctx, `DELETE FROM ozon_posting_batches WHERE tenant_id=$1`, tenant); err != nil {
			t.Errorf("remove fixture posting batches: %v", err)
		}
	}()
	now := time.Now()
	b := domain.PostingImport{Tenant: tenant, Account: "account", Since: now.Add(-time.Hour), Until: now, StartedAt: now, FinishedAt: now, Postings: []domain.PostingObservation{{PostingNumber: "p1", OrderID: "o1", OrderNumber: "order", Status: "cancelled", ObservedAt: now, ObservedTimeBasis: "detail_received", Lines: []domain.PostingLine{{OfferID: "a", SKU: "10", Quantity: 2}}}}}
	id, e := db.SavePostings(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	var n int
	results, e := db.ListPostings(ctx, domain.PostingReadScope{Tenant: tenant, Account: "account", MoyskladAccount: "test"})
	if e != nil || len(results) != 1 || results[0].BatchID != id || len(results[0].Lines) != 1 || results[0].Lines[0].Correlation.Status != "unmatched" {
		t.Fatalf("unmatched %+v %v", results, e)
	}
	b.Postings[0].Lines[0].Quantity = -1
	if _, e = db.SavePostings(ctx, b); e == nil {
		t.Fatal("bad line accepted")
	}
	if e = db.Pool.QueryRow(ctx, `SELECT count(*) FROM ozon_posting_batches WHERE tenant_id=$1`, tenant).Scan(&n); e != nil || n != 1 {
		t.Fatalf("rollback %d %v", n, e)
	}
}
