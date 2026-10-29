package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

func TestProductNamesUseBoundedBatchesAndPreserveIdentity(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != productInfoPath {
			t.Error("unexpected endpoint")
		}
		var request struct {
			IDs []string `json:"product_id"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.IDs) > 1000 {
			t.Error("invalid name batch")
		}
		items := make([]map[string]any, 0, len(request.IDs))
		for _, pid := range request.IDs {
			items = append(items, map[string]any{"id": pid, "offer_id": "offer-" + pid, "name": "Название " + pid})
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"items": items}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	products := make([]domain.OzonProduct, 1001)
	for i := range products {
		pid := strconv.Itoa(i + 1)
		products[i] = domain.OzonProduct{ProductID: pid, OfferID: "offer-" + pid, SKU: "sku-" + pid}
	}
	c := newTestClient(t, server)
	if err := c.readProductNames(context.Background(), products); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || products[1000].Name != "Название 1001" || products[1000].SKU != "sku-1001" {
		t.Fatal("batched name metadata or identity changed")
	}
}

func TestProductNamesRejectIncompleteOrSubstitutedIdentities(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{}`, `{"items":[]}`, `{"items":[{"id":2,"offer_id":"one","name":"foreign"}]}`, `{"items":[{"id":1,"offer_id":"other","name":"foreign"}]}`, `{"items":[{"id":1,"offer_id":"one","name":42}]}`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := fmt.Fprint(w, body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			c := newTestClient(t, server)
			products := []domain.OzonProduct{{ProductID: "1", OfferID: "one"}}
			if err := c.readProductNames(context.Background(), products); err == nil {
				t.Fatal("invalid name response accepted")
			}
		})
	}
}

func TestCatalogNamesShareTraversalBudget(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Path {
		case productPath:
			var request struct {
				Filter struct {
					Visibility string `json:"visibility"`
				} `json:"filter"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil {
				t.Error("invalid request")
			}
			body = `{"result":{"items":[],"total_items":0}}`
			if request.Filter.Visibility == "ALL" {
				body = `{"result":{"items":[{"product_id":1,"offer_id":"one","sku":11}],"total_items":1}}`
			}
		case productInfoPath:
			body = `{"items":[{"id":1,"offer_id":"one","name":"Товар"}]}`
		case warehousePath:
			body = `{"warehouses":[],"has_next":false}`
		case stockPath:
			body = `{"products":[],"has_next":false}`
		default:
			t.Error("unexpected endpoint")
		}
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	c := newTestClient(t, server)
	c.includeProductNames = true
	limits := Limits{Requests: 5, Attempts: 5, Pages: 5, Entities: 10, Products: 10, Warehouses: 10, Observations: 10, Postings: 10, ResponseBytes: 1 << 20, Duration: time.Second}
	snap, counts, err := c.ReadBounded(context.Background(), limits)
	if err != nil || counts.Attempts != 5 || len(snap.Products) != 1 || snap.Products[0].Name != "Товар" {
		t.Fatalf("enrichment failed: counts=%+v err=%v", counts, err)
	}
	limits.Attempts = 4
	snap, _, err = c.ReadBounded(context.Background(), limits)
	var reached *LimitError
	if !errors.As(err, &reached) || len(snap.Products) != 0 {
		t.Fatal("enrichment bypassed budget or returned partial catalog")
	}
}
