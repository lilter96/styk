//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/styk/internal/application"
	"example.com/styk/internal/domain"
	"example.com/styk/internal/infrastructure/ozon"
)

func TestPostingImportAtomicityAndRetry(t *testing.T) {
	ctx := context.Background()
	db, tenant := postingStore(t)
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Error("unexpected HTTP method")
		}
		switch r.URL.Path {
		case "/v4/posting/fbs/list":
			switch mode.Load() {
			case 1:
				fmt.Fprint(w, `{"postings":[{"posting_number":"p"}],"has_next":true}`)
			case 2:
				fmt.Fprint(w, `{"postings":[{"posting_number":"p"},{"posting_number":"p"}],"has_next":false}`)
			case 3:
				fmt.Fprint(w, `{"postings":[{"posting_number":"p"}],"has_next":false}`)
			default:
				fmt.Fprint(w, `{"postings":[{"posting_number":"p"}],"has_next":false}`)
			}
		case "/v3/posting/fbs/get":
			if mode.Load() == 3 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(time.Second):
					return
				}
			}
			fmt.Fprint(w, `{"result":{"posting_number":"p","order_id":10,"order_number":"order","status":"awaiting_packaging","products":[{"sku":11,"offer_id":"offer","quantity":3}]}}`)
		default:
			t.Error("unexpected write endpoint")
		}
	}))
	defer server.Close()
	client, err := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: "fixture-account", APIKey: "fixture-key", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	importer := application.PostingImporter{Reader: client, Store: db}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	initial, err := importer.Run(ctx, tenant, at, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		var n int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM ozon_posting_batches WHERE tenant_id=$1`, tenant).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, m := range []int32{1, 2} {
		mode.Store(m)
		if _, err = importer.Run(ctx, tenant, at, at.Add(time.Hour)); err == nil {
			t.Fatalf("mode %d accepted", m)
		}
		if count() != 1 {
			t.Fatal("partial traversal committed")
		}
	}
	mode.Store(3)
	cancelCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, err = importer.Run(cancelCtx, tenant, at, at.Add(time.Hour)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel: %v", err)
	}
	if count() != 1 {
		t.Fatal("cancelled traversal committed")
	}
	mode.Store(0)
	second, err := importer.Run(ctx, tenant, at, at.Add(time.Hour))
	if err != nil || second == initial || count() != 2 {
		t.Fatalf("retry %d %v", second, err)
	}
	rows, err := db.ListPostings(ctx, domain.PostingReadScope{Tenant: tenant, Account: "fixture-account", MoyskladAccount: "m"})
	if err != nil || len(rows) != 2 || rows[0].Observation.PostingNumber != "p" || rows[1].Observation.PostingNumber != "p" {
		t.Fatalf("append after failure %+v %v", rows, err)
	}
}

func TestPostingDatabaseRollbackAndCancellation(t *testing.T) {
	ctx := context.Background()
	db, tenant := postingStore(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := postingFixture("p", "active", at, domain.PostingLine{OfferID: "a", SKU: "1", Quantity: 1})
	batch := postingBatch(tenant, "account", at, p)
	first, err := db.SavePostings(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := batch
	duplicate.Postings = append(append([]domain.PostingObservation(nil), batch.Postings...), batch.Postings[0])
	if _, err = db.SavePostings(ctx, duplicate); err == nil {
		t.Fatal("duplicate posting in one batch accepted")
	}
	conflicting := postingBatch(tenant, "account", at, postingFixture("p", "active", at, domain.PostingLine{OfferID: "a", SKU: "1", Quantity: 1}))
	conflicting.Postings[0].OrderID = "different-order"
	if _, err = db.SavePostings(ctx, conflicting); err == nil {
		t.Fatal("posting reassigned to a different order")
	}
	batch.Postings[0].Lines = append(batch.Postings[0].Lines, domain.PostingLine{OfferID: "bad", Quantity: -1})
	if _, err = db.SavePostings(ctx, batch); err == nil {
		t.Fatal("invalid second line accepted")
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM ozon_posting_batches WHERE tenant_id=$1`, tenant).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback batch count %d %v", count, err)
	}
	// A transaction-local delay after the batch and observation inserts lets a
	// context deadline interrupt the line insert, exercising PostgreSQL rollback.
	if _, err = db.Pool.Exec(ctx, `CREATE OR REPLACE FUNCTION test_posting_line_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.offer_id='test-delay' THEN PERFORM pg_sleep(2); END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `CREATE TRIGGER test_posting_line_delay BEFORE INSERT ON ozon_posting_lines FOR EACH ROW EXECUTE FUNCTION test_posting_line_delay()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := db.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_posting_line_delay ON ozon_posting_lines`); e != nil {
			t.Error(e)
		}
		if _, e := db.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS test_posting_line_delay()`); e != nil {
			t.Error(e)
		}
	})
	batch.Postings[0].Lines = []domain.PostingLine{{OfferID: "test-delay", Quantity: 1}}
	cancelCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()
	if _, err = db.SavePostings(cancelCtx, batch); err == nil {
		t.Fatal("cancelled insertion succeeded")
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM ozon_posting_batches WHERE tenant_id=$1`, tenant).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cancel rollback count %d %v", count, err)
	}
	batch.Postings[0].Lines = []domain.PostingLine{{OfferID: "retry", Quantity: 1}}
	id, err := db.SavePostings(ctx, batch)
	if err != nil || id == first {
		t.Fatalf("retry after rollback %d %v", id, err)
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM ozon_posting_batches WHERE tenant_id=$1`, tenant).Scan(&count); err != nil || count != 2 {
		t.Fatalf("completed observations lost: %d %v", count, err)
	}
}

func TestConcurrentPostingIdentityConflict(t *testing.T) {
	ctx := context.Background()
	db, tenant := postingStore(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, order := range []string{"order-a", "order-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := postingFixture("same-posting", "active", at, domain.PostingLine{OfferID: "a", Quantity: 1})
			p.OrderID = order
			_, err := db.SavePostings(ctx, postingBatch(tenant, "seller", at, p))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successful := 0
	for err := range results {
		if err == nil {
			successful++
		}
	}
	if successful != 1 {
		t.Fatalf("successful conflicting imports %d", successful)
	}
	var batches, identities int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ozon_posting_batches WHERE tenant_id=$1),(SELECT count(*) FROM ozon_posting_identities WHERE tenant_id=$1)`, tenant).Scan(&batches, &identities); err != nil || batches != 1 || identities != 1 {
		t.Fatalf("batches %d identities %d: %v", batches, identities, err)
	}
}
