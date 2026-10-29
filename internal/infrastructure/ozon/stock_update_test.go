package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func stockTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	c, err := New(Options{BaseURL: server.URL, ClientID: "seller", APIKey: "synthetic-private-key", Timeout: time.Second, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 5, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestStockWriteSingleAttemptAndAmbiguousResult(t *testing.T) {
	for _, status := range []int{500, 502, 503, 307} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			c := stockTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != stockUpdatePath || r.Header.Get("Api-Key") != "synthetic-private-key" {
					t.Error("unexpected scoped request")
				}
				w.WriteHeader(status)
				fmt.Fprint(w, "synthetic-private-key raw vendor response")
			})
			_, err := c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 8}})
			if !errors.Is(err, ErrStockWriteUnknown) || calls.Load() != 1 || strings.Contains(err.Error(), "private-key") {
				t.Fatalf("unsafe write retry/result: %d %v", calls.Load(), err)
			}
		})
	}
}
func TestStockWritePartialResultsAndFrozenPayload(t *testing.T) {
	c := stockTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stocks []map[string]any `json:"stocks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Stocks) != 2 || body.Stocks[0]["stock"] != float64(0) || body.Stocks[0]["product_id"] != float64(123) || body.Stocks[0]["warehouse_id"] != float64(456) {
			t.Errorf("wrong frozen targets: %#v", body)
		}
		if _, exists := body.Stocks[0]["offer_id"]; exists {
			t.Error("ambiguous product/offer precedence")
		}
		fmt.Fprint(w, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]},{"product_id":124,"warehouse_id":456,"updated":false,"errors":[{"code":"PRICE_IS_NOT_SENT","message":"synthetic-private-key"}]}]}`)
	})
	result, err := c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 0}, {"124", "456", 8}})
	if err != nil || len(result) != 2 || result[0].State != "SUCCEEDED" || result[1].State != "FAILED" || result[1].SafeError != "PRICE_IS_NOT_SENT" {
		t.Fatalf("incorrect partial results: %#v %v", result, err)
	}
}
func TestStockWriteMissingMalformedOrDuplicateResultsAreUnknown(t *testing.T) {
	for _, body := range []string{`{}`, `{"result":[]}`, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true}]}`, `{"result":[{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]},{"product_id":123,"warehouse_id":456,"updated":true,"errors":[]}]}`} {
		c := stockTestClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) })
		result, err := c.UpdateStocks(context.Background(), []StockUpdate{{"123", "456", 8}})
		if err == nil && (len(result) != 1 || result[0].State != "UNKNOWN") {
			t.Fatalf("invalid response became success: %s %#v", body, result)
		}
	}
	if safeStockCode("synthetic-private-key") != "MARKETPLACE_REJECTED" {
		t.Fatal("untrusted error text exposed")
	}
}
func TestStockWriteValidationPrecedesIOAndReadbackIsReadOnly(t *testing.T) {
	var calls atomic.Int32
	c := stockTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != stockPath {
			t.Error("reconciliation attempted a write")
		}
		fmt.Fprint(w, `{"products":[{"product_id":123,"warehouse_id":456,"offer_id":"offer","free_stock":8,"reserved":2,"present":10}],"has_next":false,"cursor":""}`)
	})
	for _, items := range [][]StockUpdate{{}, {{"bad", "456", 8}}, {{"123", "456", -1}}, {{"123", "456", 8}, {"123", "456", 9}}} {
		if _, err := c.UpdateStocks(context.Background(), items); err == nil {
			t.Fatal("invalid stock update accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input reached provider")
	}
	qty, err := c.ReadStockQuantity(context.Background(), "123", "456", "offer")
	if err != nil || qty != 8 || calls.Load() != 1 {
		t.Fatalf("readback quantity: %d %v", qty, err)
	}
}
