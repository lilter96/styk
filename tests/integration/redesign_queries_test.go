//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
)

type redesignPage struct {
	Items   []json.RawMessage `json:"items"`
	Total   int               `json:"total"`
	HasNext bool              `json:"has_next"`
}

func redesignReader(t *testing.T, f stockFixtureData) func(string, int) redesignPage {
	t.Helper()
	h, err := console.FixtureHandler(f.store, "", "http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/session", strings.NewReader(`{"fixture":true}`))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "http://127.0.0.1:5173")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatal("synthetic login failed")
	}
	cookie := w.Result().Cookies()[0]
	return func(path string, status int) redesignPage {
		t.Helper()
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s: status %d: %s", path, w.Code, w.Body.String())
		}
		var page redesignPage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
}

func redesignScope(f stockFixtureData) string {
	return url.Values{"tenant": {f.req.Pool.Tenant}, "moysklad_account": {f.req.Pool.MoyskladAccount}, "ozon_account": {f.req.Target.OzonAccount}}.Encode()
}

func TestRedesignProductTotalsAndLegacyPagination(t *testing.T) {
	f := newStockFixture(t)
	seedRedesignCatalog(t, f)
	read := redesignReader(t, f)
	tests := []struct {
		name, query string
		total, rows int
		next        bool
	}{
		{"first page", "page=1&pageSize=2", 4, 2, true},
		{"last page", "page=2&pageSize=2", 4, 2, false},
		{"legacy offset", "offset=2&pageSize=2", 4, 2, false},
		{"empty page retains total", "page=9&pageSize=2", 4, 0, false},
		{"confirmed", "mapping=confirmed", 1, 1, false},
		{"unconfirmed", "mapping=unconfirmed", 3, 3, false},
		{"search", "search=extra", 3, 3, false},
		{"missing search", "search=unobserved", 0, 0, false},
		{"archived", "archived=true", 1, 1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page := read("/api/products?"+redesignScope(f)+"&"+tc.query, 200)
			if page.Total != tc.total || len(page.Items) != tc.rows || page.HasNext != tc.next {
				t.Fatalf("unexpected filtered page: %+v", page)
			}
		})
	}
}

func seedRedesignCatalog(t *testing.T, f stockFixtureData) {
	t.Helper()
	batch := f.ozon
	batch.StartedAt, batch.FinishedAt = time.Now(), time.Now()
	for i := range 3 {
		archived := i == 2
		batch.Products = append(batch.Products, domain.OzonProduct{ProductID: fmt.Sprintf("extra-%d", i), OfferID: fmt.Sprintf("extra-offer-%d", i), Archived: &archived})
	}
	if _, err := f.store.SaveOzonImport(context.Background(), batch, "redesign-pages"); err != nil {
		t.Fatal(err)
	}
}

func TestRedesignTargetSearchUsesObservedStockAndScopedTotals(t *testing.T) {
	f := newStockFixture(t)
	seedRedesignTargets(t, f)
	read := redesignReader(t, f)
	tests := []struct {
		name, query string
		total, rows int
		next        bool
	}{
		{"first", "page=1&pageSize=1", 2, 1, true},
		{"last", "page=2&pageSize=1", 2, 1, false},
		{"empty retains total", "page=3&pageSize=1", 2, 0, false},
		{"observed stock only", "inStockOnly=true", 1, 1, false},
		{"name", "searchBy=name&query=Synthetic", 1, 1, false},
		{"article", "searchBy=article&query=offer", 1, 1, false},
		{"legacy search", "search=offer", 1, 1, false},
		{"literal percent", "query=%25", 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page := read("/api/moysklad-products?"+redesignScope(f)+"&"+tc.query, 200)
			if page.Total != tc.total || len(page.Items) != tc.rows || page.HasNext != tc.next {
				t.Fatalf("unexpected target page: %+v", page)
			}
		})
	}
	read("/api/moysklad-products?"+redesignScope(f)+"&searchBy=sku", 422)
	read("/api/moysklad-products?"+redesignScope(f)+"&activeOnly=true", 422)
	read("/api/moysklad-products?"+strings.ReplaceAll(redesignScope(f), "tenant="+url.QueryEscape(f.req.Pool.Tenant), "tenant=foreign"), 404)
}

func seedRedesignTargets(t *testing.T, f stockFixtureData) {
	t.Helper()
	batch := f.source
	batch.StartedAt, batch.FinishedAt = time.Now(), time.Now()
	batch.Entities = append(batch.Entities, domain.ExternalEntity{Kind: "product", ID: "extra", Name: "Synthetic target"})
	quantity := "3"
	batch.Stock = []domain.StockObservation{{Kind: "product", ExternalID: "extra", Quantity: &quantity}}
	if _, err := f.store.SaveInventoryImport(context.Background(), batch, "redesign-targets"); err != nil {
		t.Fatal(err)
	}
}
