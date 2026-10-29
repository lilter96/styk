//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
)

type boundedCatalog struct {
	c      *ozon.Client
	limits ozon.Limits
}

func (r boundedCatalog) Read(ctx context.Context) (domain.OzonImport, error) {
	s, _, e := r.c.ReadBounded(ctx, r.limits)
	return s, e
}

type boundedPosting struct {
	c      *ozon.Client
	limits ozon.Limits
}
type invalidCatalogStore struct{ db *postgres.Store }

func (s invalidCatalogStore) SaveOzonImport(ctx context.Context, b domain.OzonImport, fingerprint string) (bool, error) {
	b.Stocks = append(b.Stocks, domain.OzonStock{ProductID: "missing", OfferID: "missing", WarehouseID: "missing"})
	return s.db.SaveOzonImport(ctx, b, fingerprint+"-rollback")
}

type invalidPostingStore struct{ db *postgres.Store }

func (s invalidPostingStore) SavePostings(ctx context.Context, b domain.PostingImport) (int64, error) {
	b.Postings[0].Lines[0].Quantity = -1
	return s.db.SavePostings(ctx, b)
}

func (r boundedPosting) ReadPostings(ctx context.Context, t string, a, b time.Time) (domain.PostingImport, error) {
	s, _, e := r.c.ReadPostingsBounded(ctx, t, a, b, r.limits)
	return s, e
}
func boundedLimits() ozon.Limits {
	return ozon.Limits{Requests: 10, Attempts: 10, Pages: 10, Entities: 10, Products: 10, Warehouses: 10, Observations: 10, Postings: 10, ResponseBytes: 4096, Duration: time.Second}
}
func boundedClient(t *testing.T, url, account string) *ozon.Client {
	t.Helper()
	c, e := ozon.New(ozon.Options{BaseURL: url, ClientID: account, APIKey: "synthetic", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestBoundedCatalogPersistenceAndFailure(t *testing.T) {
	ctx := context.Background()
	db, e := postgres.Open(ctx, os.Getenv("TEST_DATABASE_URL"), 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(db.Pool.Close)
	account := fmt.Sprintf("bounded-catalog-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Pool.Exec(context.Background(), `DELETE FROM source_imports WHERE source='ozon' AND account_id=$1`, account); err != nil {
			t.Error(err)
		}
	})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/product/list":
			fixtureText(t, w, `{"result":{"items":[],"total_items":0}}`)
		case "/v2/warehouse/list":
			fixtureText(t, w, `{"warehouses":[],"has_next":false}`)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer s.Close()
	c := boundedClient(t, s.URL, account)
	l := boundedLimits()
	imp := application.OzonImporter{Reader: boundedCatalog{c, l}, Store: db}
	if created, err := imp.Run(ctx); err != nil || !created {
		t.Fatalf("complete: %v", err)
	}
	count := func() int {
		var n int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM source_imports WHERE source='ozon' AND account_id=$1`, account).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 {
		t.Fatal("completed batch missing")
	}
	l.Pages = 1
	imp.Reader = boundedCatalog{c, l}
	if _, err := imp.Run(ctx); err == nil {
		t.Fatal("incomplete catalog accepted")
	}
	if count() != 1 {
		t.Fatal("incomplete catalog altered history")
	}
	l = boundedLimits()
	imp.Reader = boundedCatalog{c, l}
	expired, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := imp.Run(expired); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation missing", err)
	}
	if count() != 1 {
		t.Fatal("cancelled import altered history")
	}
	inflight, stopInflight := context.WithCancel(ctx)
	defer stopInflight()
	interrupted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stopInflight()
		fixtureText(t, w, `{"result":{"items":[],"total_items":0}}`)
	}))
	defer interrupted.Close()
	imp.Reader = boundedCatalog{boundedClient(t, interrupted.URL, account), l}
	if _, err := imp.Run(inflight); !errors.Is(err, context.Canceled) {
		t.Fatal("in-flight catalog cancellation missing", err)
	}
	if count() != 1 {
		t.Fatal("in-flight catalog cancellation altered history")
	}
	imp.Reader = boundedCatalog{c, l}
	imp.Store = &postgres.Store{Pool: db.Pool, Timeout: 0}
	if _, err := imp.Run(ctx); err == nil {
		t.Fatal("expired database timeout accepted")
	}
	if count() != 1 {
		t.Fatal("failed transaction altered history")
	}
	imp.Store = invalidCatalogStore{db}
	if _, err := imp.Run(ctx); err == nil {
		t.Fatal("invalid stock committed")
	}
	if count() != 1 {
		t.Fatal("transaction failure left a catalog batch")
	}
}

func TestBoundedPostingPersistenceAndFailure(t *testing.T) {
	ctx := context.Background()
	db, tenant := postingStore(t)
	account := "bounded-seller-" + tenant
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v4/posting/fbs/list":
			fixtureText(t, w, `{"postings":[{"posting_number":"synthetic"}],"has_next":false,"cursor":""}`)
		case "/v3/posting/fbs/get":
			fixtureText(t, w, `{"result":{"posting_number":"synthetic","order_id":1,"order_number":"order","status":"awaiting_packaging","products":[{"offer_id":"offer","sku":1,"quantity":1}]}}`)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer s.Close()
	c := boundedClient(t, s.URL, account)
	l := boundedLimits()
	now := time.Now()
	imp := application.PostingImporter{Reader: boundedPosting{c, l}, Store: db}
	if _, err := imp.Run(ctx, tenant, now.Add(-time.Hour), now); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM ozon_posting_batches WHERE tenant_id=$1 AND account_id=$2`, tenant, account).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 {
		t.Fatal("completed posting batch missing")
	}
	l.Requests = 1
	imp.Reader = boundedPosting{c, l}
	if _, err := imp.Run(ctx, tenant, now.Add(-time.Hour), now); err == nil {
		t.Fatal("missing detail accepted")
	}
	if count() != 1 {
		t.Fatal("partial posting batch persisted")
	}
	l = boundedLimits()
	imp.Reader = boundedPosting{c, l}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := imp.Run(cancelled, tenant, now.Add(-time.Hour), now); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation missing", err)
	}
	if count() != 1 {
		t.Fatal("cancelled posting batch persisted")
	}
	inflight, stopInflight := context.WithCancel(ctx)
	defer stopInflight()
	interrupted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stopInflight()
		fixtureText(t, w, `{"postings":[],"has_next":false}`)
	}))
	defer interrupted.Close()
	imp.Reader = boundedPosting{boundedClient(t, interrupted.URL, account), l}
	if _, err := imp.Run(inflight, tenant, now.Add(-time.Hour), now); !errors.Is(err, context.Canceled) {
		t.Fatal("in-flight posting cancellation missing", err)
	}
	if count() != 1 {
		t.Fatal("in-flight posting cancellation altered history")
	}
	imp.Reader = boundedPosting{c, l}
	imp.Store = &postgres.Store{Pool: db.Pool, Timeout: 0}
	if _, err := imp.Run(ctx, tenant, now.Add(-time.Hour), now); err == nil {
		t.Fatal("expired transaction accepted")
	}
	if count() != 1 {
		t.Fatal("failed transaction altered posting history")
	}
	imp.Store = invalidPostingStore{db}
	if _, err := imp.Run(ctx, tenant, now.Add(-time.Hour), now); err == nil {
		t.Fatal("invalid position committed")
	}
	if count() != 1 {
		t.Fatal("transaction failure left a posting batch")
	}
}
