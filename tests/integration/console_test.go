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

	"example.com/styk/internal/console"
	"example.com/styk/internal/domain"
)

func TestConsoleScopedAuthenticatedReads(t *testing.T) {
	f := newStockFixture(t)
	secret := strings.Repeat("s", 32)
	h, e := console.Handler(f.store, secret, "http://127.0.0.1:5173")
	if e != nil {
		t.Fatal(e)
	}
	request := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		if method == "POST" {
			r.Header.Set("Origin", "http://127.0.0.1:5173")
			r.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/registrations?tenant="+url.QueryEscape(f.req.Pool.Tenant), "", nil); w.Code != 401 {
		t.Fatal("missing authentication")
	}
	w := request("POST", "/api/session", fmt.Sprintf(`{"secret":%q}`, secret), nil)
	if w.Code != 204 {
		t.Fatal("login failed")
	}
	cookie := w.Result().Cookies()[0]
	q := url.Values{"tenant": {f.req.Pool.Tenant}, "ozon_account": {f.req.Target.OzonAccount}, "moysklad_account": {f.req.Pool.MoyskladAccount}}
	w = request("GET", "/api/products?"+q.Encode(), "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"offer_id":"offer"`) {
		t.Fatal("scoped product unavailable")
	}
	if !strings.Contains(w.Body.String(), `"match_status":"CONFIRMED"`) {
		t.Fatal("confirmed matching status not computed")
	}
	w = request("GET", "/api/moysklad-products?"+q.Encode()+"&search=offer", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"variant"`) || !strings.Contains(w.Body.String(), `"article":"offer"`) {
		t.Fatal("scoped target search failed")
	}
	w = request("GET", "/api/product-mapping-history?"+q.Encode()+"&product=product", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"version":1`) || !strings.Contains(w.Body.String(), `"provenance":"fixture product confirmation"`) {
		t.Fatal("mapping audit history missing")
	}
	badScope := url.Values{"tenant": {f.req.Pool.Tenant + "-other"}, "ozon_account": {f.req.Target.OzonAccount}, "moysklad_account": {f.req.Pool.MoyskladAccount}}
	w = request("GET", "/api/moysklad-products?"+badScope.Encode(), "", cookie)
	if w.Code != 404 || strings.Contains(w.Body.String(), "offer") {
		t.Fatal("cross-tenant search exposed")
	}
	w = request("GET", "/api/product-mapping-history?"+badScope.Encode()+"&product=product", "", cookie)
	if w.Code != 404 || strings.Contains(w.Body.String(), "fixture product confirmation") {
		t.Fatal("cross-tenant history exposed")
	}
	q.Set("search", "absent")
	w = request("GET", "/api/products?"+q.Encode(), "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal("product filter failed")
	}
	q.Del("search")
	w = request("GET", "/api/diagnostics?"+q.Encode(), "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"decision_status":"UNKNOWN"`) || !strings.Contains(w.Body.String(), `"decision_reasons"`) || strings.Contains(w.Body.String(), secret) {
		t.Fatal("diagnostic evidence or secret isolation failed")
	}
	q.Set("tenant", f.req.Pool.Tenant+"-other")
	w = request("GET", "/api/products?"+q.Encode(), "", cookie)
	if w.Code != 404 || strings.Contains(w.Body.String(), "offer") {
		t.Fatal("cross-tenant product access")
	}
	q = url.Values{"tenant": {f.req.Pool.Tenant}, "source": {"ozon"}, "account": {f.req.Target.OzonAccount}}
	w = request("GET", "/api/warehouses?"+q.Encode(), "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"stock_rows":1`) {
		t.Fatal("warehouse stock evidence unavailable")
	}
	q.Set("warehouse", f.req.Target.WarehouseID)
	w = request("GET", "/api/stock?"+q.Encode(), "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"present":"5"`) || !strings.Contains(w.Body.String(), `"stock":null`) {
		t.Fatal("source stock fields were collapsed or missing")
	}
	if _, err := f.store.Pool.Exec(context.Background(), `UPDATE ozon_fbs_stock_observations SET present=0 WHERE import_id=(SELECT id FROM source_imports WHERE source='ozon' AND account_id=$1 ORDER BY id DESC LIMIT 1)`, f.req.Target.OzonAccount); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "/api/stock?"+q.Encode(), "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"present":"0"`) {
		t.Fatal("explicit zero was not preserved")
	}
	q.Set("warehouse", "unobserved")
	w = request("GET", "/api/stock?"+q.Encode(), "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal("missing stock treated as an observation")
	}
}

func TestConsoleAmbiguousCandidatesAreNotConfirmed(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	m := f.source
	m.StartedAt = now
	m.FinishedAt = now.Add(time.Second)
	m.Entities = append(m.Entities, domain.ExternalEntity{Kind: "product", ID: "duplicate-a", Article: "duplicate"}, domain.ExternalEntity{Kind: "variant", ID: "duplicate-b", Article: "duplicate", ParentID: "parent-product"})
	if _, e := f.store.SaveInventoryImport(ctx, m, "console-match-moy"); e != nil {
		t.Fatal(e)
	}
	o := f.ozon
	o.StartedAt = now
	o.FinishedAt = now.Add(time.Second)
	o.Products = append(o.Products, domain.OzonProduct{ProductID: "unmapped", OfferID: "duplicate"})
	if _, e := f.store.SaveOzonImport(ctx, o, "console-match-ozon"); e != nil {
		t.Fatal(e)
	}
	secret := strings.Repeat("s", 32)
	h, e := console.Handler(f.store, secret, "http://127.0.0.1:5173")
	if e != nil {
		t.Fatal(e)
	}
	login := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/session", strings.NewReader(fmt.Sprintf(`{"secret":%q}`, secret)))
	login.RemoteAddr = "127.0.0.1:1234"
	login.Header.Set("Origin", "http://127.0.0.1:5173")
	login.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, login)
	if w.Code != 204 {
		t.Fatal("login")
	}
	q := url.Values{"tenant": {f.req.Pool.Tenant}, "ozon_account": {f.req.Target.OzonAccount}, "moysklad_account": {f.req.Pool.MoyskladAccount}, "mapping": {"unconfirmed"}}
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/products?"+q.Encode(), nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.AddCookie(w.Result().Cookies()[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"match_status":"AMBIGUOUS"`) || !strings.Contains(w.Body.String(), `"candidate_count":2`) || !strings.Contains(w.Body.String(), `"sku":null`) {
		t.Fatal("ambiguous candidates or missing SKU not preserved")
	}
	q.Set("search", "duplicate-b")
	search := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/moysklad-products?"+q.Encode(), nil)
	search.RemoteAddr = "127.0.0.1:1234"
	search.AddCookie(r.Cookies()[0])
	result := httptest.NewRecorder()
	h.ServeHTTP(result, search)
	if result.Code != 200 || !strings.Contains(result.Body.String(), `"parent_id":"parent-product"`) {
		t.Fatal("variant parent identity not preserved")
	}
}

func TestConsoleProductPagination(t *testing.T) {
	f := newStockFixture(t)
	now := time.Now().UTC()
	batch := f.ozon
	batch.StartedAt, batch.FinishedAt = now, now.Add(time.Second)
	for i := 0; i < 53; i++ {
		batch.Products = append(batch.Products, domain.OzonProduct{ProductID: fmt.Sprintf("p-%03d", i), OfferID: fmt.Sprintf("offer-%03d", i)})
	}
	if _, err := f.store.SaveOzonImport(context.Background(), batch, "console-pagination"); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 32)
	h, err := console.Handler(f.store, secret, "http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/session", strings.NewReader(fmt.Sprintf(`{"secret":%q}`, secret)))
	login.RemoteAddr = "127.0.0.1:1234"
	login.Header.Set("Origin", "http://127.0.0.1:5173")
	login.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, login)
	if w.Code != 204 {
		t.Fatal("login failed")
	}
	cookie := w.Result().Cookies()[0]
	q := url.Values{"tenant": {f.req.Pool.Tenant}, "ozon_account": {f.req.Target.OzonAccount}, "moysklad_account": {f.req.Pool.MoyskladAccount}}
	page := func(offset string) struct {
		Items []struct {
			ProductID string `json:"product_id"`
		} `json:"items"`
		HasNext bool `json:"has_next"`
	} {
		q.Set("offset", offset)
		r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/products?"+q.Encode(), nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("page status %d", w.Code)
		}
		var v struct {
			Items []struct {
				ProductID string `json:"product_id"`
			} `json:"items"`
			HasNext bool `json:"has_next"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	first, second := page("0"), page("50")
	if len(first.Items) != 50 || !first.HasNext || len(second.Items) != 4 || second.HasNext || first.Items[49].ProductID == second.Items[0].ProductID {
		t.Fatal("invalid product pagination")
	}
}

func TestConsoleHistoryRespectsImportHistoryRegistration(t *testing.T) {
	f := newStockFixtureWithHistory(t, false)
	secret := strings.Repeat("s", 32)
	h, err := console.Handler(f.store, secret, "http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/session", strings.NewReader(fmt.Sprintf(`{"secret":%q}`, secret)))
	login.RemoteAddr = "127.0.0.1:1234"
	login.Header.Set("Origin", "http://127.0.0.1:5173")
	login.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, login)
	if w.Code != 204 {
		t.Fatal("login failed")
	}
	q := url.Values{"tenant": {f.req.Pool.Tenant}, "ozon_account": {f.req.Target.OzonAccount}, "moysklad_account": {f.req.Pool.MoyskladAccount}, "product": {"product"}}
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/product-mapping-history?"+q.Encode(), nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.AddCookie(w.Result().Cookies()[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal("pre-registration mapping history was exposed")
	}
}
