//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/postgres"
)

func TestMoyskladIngestion(t *testing.T) {
	ctx := context.Background()
	s, err := postgres.Open(ctx, os.Getenv("TEST_DATABASE_URL"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Pool.Close)
	account := fmt.Sprintf("test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, e := s.Pool.Exec(context.Background(), "DELETE FROM source_imports WHERE account_id=$1", account); e != nil {
			t.Error(e)
		}
	})
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" || r.Method != "GET" {
			t.Error("unexpected request")
		}
		switch r.URL.Path {
		case "/entity/product":
			if mode.Load() == 2 {
				fmt.Fprint(w, `{"meta":{"size":2,"limit":100,"offset":0},"rows":[{"id":"duplicate"},{"id":"duplicate"}]}`)
				return
			}
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":"p1","accountId":%q,"name":"test"}]}`, account)
		case "/entity/variant":
			fmt.Fprint(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":"v1"}]}`)
		case "/entity/store":
			fmt.Fprint(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":"w1"}]}`)
		case "/report/stock/bystore":
			if r.URL.Query().Get("filter") != "stockMode=all" || r.URL.Query().Get("groupBy") != "variant" {
				t.Error("wrong warehouse report scope")
			}
			if mode.Load() == 3 {
				fmt.Fprint(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"type":"product","href":"/entity/product/p1"},"stockByStore":[{"meta":{"type":"store","href":"/entity/store/w1"},"stock":0,"reserve":0,"inTransit":0},{"meta":{"type":"store","href":"/entity/store/w1"},"stock":1,"reserve":0,"inTransit":0}]}]}`)
				return
			}
			if mode.Load() == 1 {
				if r.URL.Query().Get("offset") == "0" {
					fmt.Fprint(w, `{"meta":{"size":2,"limit":1,"offset":0},"rows":[{"meta":{"type":"product","href":"/entity/product/p1"},"stockByStore":[]}]}`)
				} else {
					fmt.Fprint(w, `{"meta":{"size":2,"limit":1,"offset":1},"rows":[]}`)
				}
				return
			}
			if mode.Load() == 4 {
				fmt.Fprint(w, `{"meta":{"size":2,"limit":100,"offset":0},"rows":[{"meta":{"type":"variant","href":"/entity/variant/v1"},"stockByStore":[]},{"meta":{"type":"variant","href":"/entity/variant/v1"},"stockByStore":[]}]}`)
				return
			}
			fmt.Fprint(w, `{"meta":{"size":2,"limit":100,"offset":0},"rows":[{"meta":{"type":"product","href":"https://api.moysklad.ru/api/remap/1.2/entity/product/p1"},"stockByStore":[{"meta":{"type":"store","href":"https://api.moysklad.ru/api/remap/1.2/entity/store/w1"},"stock":0,"reserve":2,"inTransit":1},{"meta":{"type":"store","href":"https://api.moysklad.ru/api/remap/1.2/entity/store/w2"},"stock":-4.5,"reserve":0,"inTransit":3}]},{"meta":{"type":"variant","href":"https://api.moysklad.ru/api/remap/1.2/entity/variant/v1"},"stockByStore":[{"meta":{"type":"store","href":"https://api.moysklad.ru/api/remap/1.2/entity/store/w1"},"stock":3,"reserve":1,"inTransit":0}]}]}`)
		case "/report/stock/all":
			if r.URL.Query().Get("filter") != "stockMode=all;quantityMode=all" {
				t.Error("missing filters")
			}
			fmt.Fprint(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/p1","type":"product"},"stock":4.5,"reserve":2,"inTransit":1,"quantity":3.5}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c, err := moysklad.New(moysklad.Options{BaseURL: server.URL + "/", Token: "fixture-token", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 2, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	imp := application.Importer{Reader: c, Store: s}
	for n := 0; n < 2; n++ {
		created, e := imp.Run(ctx, account)
		if e != nil || created != (n == 0) {
			t.Fatalf("import %d created %v err %v", n, created, e)
		}
	}
	var count int
	var stock, quantity string
	err = s.Pool.QueryRow(ctx, `SELECT count(*), min(o.stock)::text,min(o.quantity)::text FROM source_imports i JOIN source_stock_observations o ON o.import_id=i.id WHERE i.account_id=$1`, account).Scan(&count, &stock, &quantity)
	if err != nil || count != 1 || stock != "4.5" || quantity != "3.5" {
		t.Fatalf("persisted: %d %s %s %v", count, stock, quantity, err)
	}
	var records int
	err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM source_warehouse_stock_observations o JOIN source_imports i ON i.id=o.import_id WHERE i.account_id=$1 AND o.observed_at=i.finished_at AND o.quantity IS NULL AND ((o.kind='product' AND o.external_id='p1' AND o.warehouse_id='w1' AND o.stock=0 AND o.reserve=2 AND o.in_transit=1) OR (o.kind='product' AND o.external_id='p1' AND o.warehouse_id='w2' AND o.stock=-4.5) OR (o.kind='variant' AND o.external_id='v1' AND o.warehouse_id='w1' AND o.stock=3))`, account).Scan(&records)
	if err != nil || records != 3 {
		t.Fatalf("warehouse observations: %d %v", records, err)
	}
	err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM source_warehouse_stock_observations o JOIN source_imports i ON i.id=o.import_id WHERE i.account_id=$1 AND o.kind='variant' AND o.external_id='v1' AND o.warehouse_id='w2'`, account).Scan(&records)
	if err != nil || records != 0 {
		t.Fatalf("missing row inferred: %d %v", records, err)
	}
	for _, v := range []int32{1, 2, 3, 4} {
		mode.Store(v)
		if _, err = imp.Run(ctx, account); err == nil {
			t.Fatalf("mode %d accepted", v)
		}
	}
	mode.Store(0)
	err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM source_imports WHERE account_id=$1`, account).Scan(&records)
	if err != nil || records != 1 {
		t.Fatalf("incomplete import persisted: %d %v", records, err)
	}
	// A bad observation must roll back the import header and preceding entities.
	bad := "not-a-number"
	snap := domain.InventoryImport{Account: account, StartedAt: time.Now(), FinishedAt: time.Now(), Entities: []domain.ExternalEntity{{Kind: "product", ID: "p"}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: "p", WarehouseID: "w1", Stock: &bad, Reserve: &bad, InTransit: &bad}}}
	_, err = s.SaveInventoryImport(ctx, snap, "invalid")
	if err == nil {
		t.Fatal("invalid number accepted")
	}
	err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM source_imports WHERE account_id=$1 AND fingerprint='invalid'", account).Scan(&count)
	if err != nil || count != 0 {
		t.Fatal("failed import did not roll back")
	}
	if !strings.Contains(stock, "4.5") {
		t.Fatal("stock mismatch")
	}
}
