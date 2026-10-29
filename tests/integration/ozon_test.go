//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
)

func TestOzonImport(t *testing.T) {
	ctx := context.Background()
	db, err := postgres.Open(ctx, os.Getenv("TEST_DATABASE_URL"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	account := fmt.Sprintf("ozon-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.Pool.Exec(context.Background(), `DELETE FROM source_imports WHERE source='ozon' AND account_id=$1`, account); err != nil {
			t.Error(err)
		}
	})
	var partial atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Client-Id") != account || r.Header.Get("Api-Key") != "fixture-key" {
			t.Error("bad auth")
		}
		switch r.URL.Path {
		case "/v3/product/list":
			var request struct {
				Filter struct {
					Visibility string `json:"visibility"`
				} `json:"filter"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request.Filter.Visibility == "ARCHIVED" {
				fixtureText(t, w, `{"result":{"items":[],"total_items":0}}`)
			} else {
				fixtureText(t, w, `{"result":{"items":[{"product_id":1,"offer_id":"offer","sku":99,"archived":false}],"total_items":1}}`)
			}
		case "/v2/warehouse/list":
			if partial.Load() {
				fixtureText(t, w, `{"warehouses":[]}`)
			} else {
				fixtureText(t, w, `{"warehouses":[{"warehouse_id":100,"name":"one","is_rfbs":false}],"has_next":false}`)
			}
		case "/v2/product/info/stocks-by-warehouse/fbs":
			fixtureText(t, w, `{"products":[{"product_id":1,"offer_id":"offer","sku":99,"warehouse_id":100,"present":10,"reserved":2,"free_stock":8}],"has_next":false}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, err := ozon.New(ozon.Options{BaseURL: server.URL, ClientID: account, APIKey: "fixture-key", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	imp := application.OzonImporter{Reader: c, Store: db}
	for n := 0; n < 2; n++ {
		created, e := imp.Run(ctx)
		if e != nil || created != (n == 0) {
			t.Fatalf("repeated import %v %v", created, e)
		}
	}
	partial.Store(true)
	if _, err = imp.Run(ctx); err == nil {
		t.Fatal("partial import accepted")
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM source_imports WHERE source='ozon' AND account_id=$1`, account).Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial batch persisted: %d %v", count, err)
	}
	snap := domain.OzonImport{Account: account, StartedAt: time.Now(), FinishedAt: time.Now(), Products: []domain.OzonProduct{{ProductID: "1", OfferID: "offer", SKU: "99"}}, Stocks: []domain.OzonStock{{ProductID: "missing", OfferID: "offer", WarehouseID: "100"}}}
	if _, err = db.SaveOzonImport(ctx, snap, "rollback-test"); err == nil {
		t.Fatal("invalid reference accepted")
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM source_imports WHERE source='ozon' AND account_id=$1 AND fingerprint='rollback-test'`, account).Scan(&count); err != nil || count != 0 {
		t.Fatalf("transaction not rolled back: %d %v", count, err)
	}
	snap.Stocks = []domain.OzonStock{{ProductID: "1", OfferID: "offer", SKU: "99", WarehouseID: "100", Present: 10, Reserved: 2, FreeStock: 8}, {ProductID: "1", OfferID: "offer", SKU: "99", WarehouseID: "200", Present: 0, Reserved: 0, FreeStock: 0}}
	if created, e := db.SaveOzonImport(ctx, snap, "stock-test"); e != nil || !created {
		t.Fatalf("save stock %v %v", created, e)
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM ozon_fbs_stock_observations o JOIN source_imports i ON i.id=o.import_id JOIN ozon_import_batches b ON b.import_id=i.id WHERE i.account_id=$1 AND i.fingerprint='stock-test' AND o.product_id='1' AND o.sku='99' AND o.offer_id='offer' AND b.traversal_status='completed' AND o.observed_at=i.finished_at`, account).Scan(&count); err != nil || count != 2 {
		t.Fatalf("persisted stock %d %v", count, err)
	}
}
