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
	for _, path := range []string{"/api/products?tenant=" + url.QueryEscape(f.req.Pool.Tenant), "/api/warehouses?tenant=" + url.QueryEscape(f.req.Pool.Tenant), "/api/stock?tenant=" + url.QueryEscape(f.req.Pool.Tenant), "/api/diagnostics?tenant=" + url.QueryEscape(f.req.Pool.Tenant)} {
		if w := request("GET", path, "", nil); w.Code != 401 {
			t.Fatalf("unauthenticated business read accepted: %s", path)
		}
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
	q.Set("tenant", f.req.Pool.Tenant)
	q.Set("ozon_account", f.req.Target.OzonAccount+"-other")
	if w = request("GET", "/api/products?"+q.Encode(), "", cookie); w.Code != 404 {
		t.Fatal("cross-account product access")
	}
	q = url.Values{"tenant": {f.req.Pool.Tenant}, "source": {"ozon"}, "account": {f.req.Target.OzonAccount}}
	w = request("GET", "/api/warehouses?"+q.Encode(), "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"stock_rows":1`) {
		t.Fatal("warehouse stock evidence unavailable")
	}
	q.Set("tenant", f.req.Pool.Tenant+"-other")
	if w = request("GET", "/api/warehouses?"+q.Encode(), "", cookie); w.Code != 404 {
		t.Fatal("cross-tenant warehouse access")
	}
	q.Set("tenant", f.req.Pool.Tenant)
	q.Set("account", f.req.Target.OzonAccount+"-other")
	if w = request("GET", "/api/warehouses?"+q.Encode(), "", cookie); w.Code != 404 {
		t.Fatal("cross-account warehouse access")
	}
	q.Set("account", f.req.Target.OzonAccount)
	q.Set("warehouse", f.req.Target.WarehouseID)
	w = request("GET", "/api/stock?"+q.Encode(), "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"present":"5"`) || !strings.Contains(w.Body.String(), `"stock":null`) {
		t.Fatal("source stock fields were collapsed or missing")
	}
	q.Set("account", f.req.Target.OzonAccount+"-other")
	if w = request("GET", "/api/stock?"+q.Encode(), "", cookie); w.Code != 404 {
		t.Fatal("cross-account stock access")
	}
	q.Set("account", f.req.Target.OzonAccount)
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
	m.Entities = append(m.Entities, domain.ExternalEntity{Kind: "product", ID: "duplicate-a", Article: "\u00a0duplicate\u00a0"}, domain.ExternalEntity{Kind: "variant", ID: "duplicate-b", Name: "Synthetic variant name", Article: "duplicate", ParentID: "parent-product"})
	// Unrelated catalog size must not cause matching evidence for this page to
	// be truncated. Unicode whitespace follows the Go matcher exactly.
	for i := 0; i < 2100; i++ {
		m.Entities = append(m.Entities, domain.ExternalEntity{Kind: "product", ID: fmt.Sprintf("other-%04d", i), Article: fmt.Sprintf("other-%04d", i)})
	}
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
	q.Set("search", "Synthetic variant name")
	searchProducts := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/products?"+q.Encode(), nil)
	searchProducts.RemoteAddr = "127.0.0.1:1234"
	searchProducts.AddCookie(r.Cookies()[0])
	byName := httptest.NewRecorder()
	h.ServeHTTP(byName, searchProducts)
	if byName.Code != 200 || !strings.Contains(byName.Body.String(), `"offer_id":"duplicate"`) || !strings.Contains(byName.Body.String(), `"name":"Synthetic variant name"`) || !strings.Contains(byName.Body.String(), `"candidate_count":2`) {
		t.Fatalf("candidate name search or bounded matching failed: %d %s", byName.Code, byName.Body.String())
	}
	search := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/moysklad-products?"+q.Encode(), nil)
	search.RemoteAddr = "127.0.0.1:1234"
	search.AddCookie(r.Cookies()[0])
	result := httptest.NewRecorder()
	h.ServeHTTP(result, search)
	if result.Code != 200 || !strings.Contains(result.Body.String(), `"parent_id":"parent-product"`) || !strings.Contains(result.Body.String(), `"name":"Synthetic variant name"`) {
		t.Fatal("variant name or parent identity not preserved")
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

func TestWarehouseReviewScopesEvidenceAndIsReadOnly(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	ms := f.source
	ms.Entities = append(ms.Entities, domain.ExternalEntity{Kind: "store", ID: "new-source", Name: "Synthetic source"})
	ms.StartedAt, ms.FinishedAt = time.Now(), time.Now()
	oz := f.ozon
	fbs := false
	oz.Warehouses = append(oz.Warehouses, domain.OzonWarehouse{ID: "new-target", Name: "Synthetic FBS", IsRFBS: &fbs})
	oz.StartedAt, oz.FinishedAt = time.Now(), time.Now()
	if _, err := f.store.SaveInventoryImport(ctx, ms, "warehouse-review-new-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SaveOzonImport(ctx, oz, "warehouse-review-new-target"); err != nil {
		t.Fatal(err)
	}
	var initialTasks int
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_tasks`).Scan(&initialTasks); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("w", 32)
	h, err := console.Handler(f.store, secret, "http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
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
	login := request("POST", "/api/session", fmt.Sprintf(`{"secret":%q}`, secret), nil)
	if login.Code != 204 {
		t.Fatal("login")
	}
	cookie := login.Result().Cookies()[0]
	q := url.Values{"tenant": {f.req.Pool.Tenant}, "moysklad_account": {f.req.Pool.MoyskladAccount}, "ozon_account": {f.req.Target.OzonAccount}, "moysklad_warehouse": {f.req.Pool.WarehouseID}, "ozon_warehouse": {f.req.Target.WarehouseID}}
	path := func() string { return "/api/warehouse-mapping-review?" + q.Encode() }
	q.Set("moysklad_warehouse", "new-source")
	q.Set("ozon_warehouse", "new-target")
	if w := request("GET", path(), "", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"ELIGIBLE_FOR_REVIEW"`) || !strings.Contains(w.Body.String(), `"stock_rows":0`) {
		t.Fatal("unmapped FBS pair or missing stock evidence incorrect")
	}
	q.Set("moysklad_warehouse", f.req.Pool.WarehouseID)
	q.Set("ozon_warehouse", f.req.Target.WarehouseID)
	if w := request("GET", path(), "", nil); w.Code != 401 {
		t.Fatal("unauthenticated review")
	}
	w := request("GET", path(), "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"ALREADY_MAPPED"`) || !strings.Contains(w.Body.String(), `"history":[{`) || !strings.Contains(w.Body.String(), `"stock_rows":1`) {
		t.Fatalf("mapping review: %d %s", w.Code, w.Body.String())
	}
	q.Set("tenant", f.req.Pool.Tenant+"-other")
	if w = request("GET", path(), "", cookie); w.Code != 404 {
		t.Fatal("cross-tenant access")
	}
	q.Set("tenant", f.req.Pool.Tenant)
	q.Set("ozon_account", f.req.Target.OzonAccount+"-other")
	if w = request("GET", path(), "", cookie); w.Code != 404 {
		t.Fatal("cross-account access")
	}
	emptyAccount := f.req.Target.OzonAccount + "-empty"
	if err = f.store.RegisterStockConnection(ctx, f.req.Pool.Tenant, "ozon", emptyAccount, "synthetic no import", true); err != nil {
		t.Fatal(err)
	}
	q.Set("ozon_account", emptyAccount)
	if w = request("GET", path(), "", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"MISSING_SOURCE_EVIDENCE"`) || !strings.Contains(w.Body.String(), `"ozon":null`) {
		t.Fatal("missing completed import not reported")
	}
	q.Set("ozon_account", f.req.Target.OzonAccount)
	q.Set("moysklad_warehouse", "missing")
	q.Set("ozon_warehouse", "missing")
	if w = request("GET", path(), "", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"MISSING_SOURCE_EVIDENCE"`) {
		t.Fatal("missing evidence not reported")
	}
	q.Set("ozon_warehouse", f.req.Target.WarehouseID)
	if w = request("GET", path(), "", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"MAPPING_CONFLICT"`) {
		t.Fatal("target conflict not reported")
	}
	q.Set("moysklad_warehouse", f.req.Pool.WarehouseID)
	if _, err = f.store.Pool.Exec(ctx, `UPDATE ozon_warehouse_observations SET is_rfbs=true WHERE warehouse_id=$1 AND import_id=(SELECT id FROM source_imports WHERE source='ozon' AND account_id=$2 ORDER BY id DESC LIMIT 1)`, f.req.Target.WarehouseID, f.req.Target.OzonAccount); err != nil {
		t.Fatal(err)
	}
	if w = request("GET", path(), "", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"UNSUPPORTED_WAREHOUSE"`) {
		t.Fatal("rFBS not rejected")
	}
	var n, history int
	if err = f.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1),(SELECT count(*) FROM warehouse_mapping_history WHERE tenant_id=$1)`, f.req.Pool.Tenant).Scan(&n, &history); err != nil || n != 1 || history != 1 {
		t.Fatalf("review changed persisted mappings: %d %d", n, history)
	}
	var finalTasks int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_tasks`).Scan(&finalTasks); err != nil || finalTasks != initialTasks {
		t.Fatal("review scheduled work")
	}
	ms.Entities = f.source.Entities
	ms.StartedAt, ms.FinishedAt = time.Now(), time.Now()
	if _, err := f.store.SaveInventoryImport(ctx, ms, "warehouse-review-source-removed"); err != nil {
		t.Fatal(err)
	}
	q.Set("moysklad_warehouse", "new-source")
	q.Set("ozon_warehouse", "new-target")
	if w = request("GET", path(), "", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"MISSING_SOURCE_EVIDENCE"`) {
		t.Fatal("review used a warehouse from an older import")
	}
}

func TestWarehouseReviewDoesNotExposePreRegistrationHistory(t *testing.T) {
	f := newStockFixtureWithHistory(t, false)
	secret := strings.Repeat("q", 32)
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
		t.Fatal("login")
	}
	q := url.Values{"tenant": {f.req.Pool.Tenant}, "moysklad_account": {f.req.Pool.MoyskladAccount}, "ozon_account": {f.req.Target.OzonAccount}, "moysklad_warehouse": {f.req.Pool.WarehouseID}, "ozon_warehouse": {f.req.Target.WarehouseID}}
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/warehouse-mapping-review?"+q.Encode(), nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.AddCookie(w.Result().Cookies()[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "fixture warehouse confirmation") || !strings.Contains(w.Body.String(), `"history":[]`) || !strings.Contains(w.Body.String(), `"moysklad":null`) || !strings.Contains(w.Body.String(), `"status":"MAPPING_CONFLICT"`) {
		t.Fatal("pre-registration evidence leaked or hidden mapping was treated as eligible")
	}
}
