//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"example.com/styk/internal/application"
	"example.com/styk/internal/domain"
	"example.com/styk/internal/infrastructure/moysklad"
	"example.com/styk/internal/postgres"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" || r.Method != "GET" {
			t.Error("unexpected request")
		}
		switch r.URL.Path {
		case "/entity/product":
			fmt.Fprintf(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":"p1","accountId":%q,"name":"test"}]}`, account)
		case "/entity/variant":
			fmt.Fprint(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":"v1"}]}`)
		case "/entity/store":
			fmt.Fprint(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":"w1"}]}`)
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
	// A bad observation must roll back the import header and preceding entities.
	bad := "not-a-number"
	snap := domain.InventoryImport{Account: account, StartedAt: time.Now(), FinishedAt: time.Now(), Entities: []domain.ExternalEntity{{Kind: "product", ID: "p"}}, Stock: []domain.StockObservation{{Kind: "product", ExternalID: "p", Stock: &bad}}}
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
