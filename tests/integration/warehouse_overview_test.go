//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lilter96/styk/internal/console"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

func overviewScope(f stockFixtureData) postgres.MatchScope {
	return postgres.MatchScope{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount}
}
func readOverview(t *testing.T, f stockFixtureData, r postgres.WarehouseReadRequest) postgres.WarehouseOverviewPage {
	t.Helper()
	result, err := f.store.WarehouseOverview(context.Background(), overviewScope(f), r)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestWarehouseOverviewCountsSearchAndStablePages(t *testing.T) {
	f := newStockFixture(t)
	oz := f.ozon
	for i := 0; i < 56; i++ {
		oz.Warehouses = append(oz.Warehouses, domain.OzonWarehouse{ID: fmt.Sprintf("extra-%03d", i), Name: fmt.Sprintf("Регион %03d", i), IsRFBS: oz.Warehouses[0].IsRFBS})
	}
	if _, err := f.store.SaveOzonImport(context.Background(), oz, "overview-many"); err != nil {
		t.Fatal(err)
	}
	first := readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25})
	if first.Counts.Total != 57 || first.Counts.Mapped != 1 || first.Counts.Unmapped != 56 || len(first.Items) != 25 || !first.HasNext {
		t.Fatalf("bad first page: %+v", first)
	}
	next := readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25, Cursor: first.Items[24].ID})
	if len(next.Items) != 25 || next.Items[0].ID <= first.Items[24].ID || next.Counts != first.Counts {
		t.Fatal("unstable cursor or counts")
	}
	last := readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25, Offset: 50})
	if len(last.Items) != 7 || last.HasNext {
		t.Fatalf("bad last page: %+v", last)
	}
	search := readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25, Search: "Регион 055", Filter: "unmapped"})
	if search.Total != 1 || search.Items[0].ID != "extra-055" || search.Counts != first.Counts {
		t.Fatal("search filtered only the loaded page or changed counts")
	}
	empty := readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25, Search: "absent"})
	if empty.Total != 0 || empty.Counts != first.Counts || len(empty.Items) != 0 {
		t.Fatal("empty page lost aggregate counts")
	}
}
func TestWarehouseOverviewConfirmedNamesAndIsolation(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	if _, err := f.store.Pool.Exec(ctx, `UPDATE source_entities SET name='Подтверждённый склад' WHERE import_id IN(SELECT id FROM source_imports WHERE account_id=$1) AND kind='store'`, f.req.Pool.MoyskladAccount); err != nil {
		t.Fatal(err)
	}
	page := readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25})
	if page.Items[0].StoreName != "Подтверждённый склад" || page.Items[0].State != "mapped" {
		t.Fatal("confirmed name not joined")
	}
	tests := []struct {
		name  string
		scope postgres.MatchScope
	}{{"tenant", postgres.MatchScope{Tenant: "other", MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount}}, {"account", postgres.MatchScope{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: "other"}}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.store.WarehouseOverview(ctx, tc.scope, postgres.WarehouseReadRequest{Limit: 25}); !errors.Is(err, postgres.ErrReviewScope) {
				t.Fatalf("scope accepted: %v", err)
			}
			if _, err := f.store.WarehouseCandidates(ctx, tc.scope, postgres.WarehouseReadRequest{Limit: 25}); !errors.Is(err, postgres.ErrReviewScope) {
				t.Fatalf("candidate scope accepted: %v", err)
			}
		})
	}
	hidden := readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25, Warehouse: "foreign-warehouse"})
	if len(hidden.Items) != 0 {
		t.Fatal("foreign warehouse resolved")
	}
}
func TestWarehouseOverviewRegistrationAndMissingEvidence(t *testing.T) {
	f := newStockFixtureWithHistory(t, false)
	ctx := context.Background()
	page := readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25})
	if page.Counts.Total != 0 || page.OzonImported || page.MoyImported {
		t.Fatal("old registrations exposed evidence")
	}
	f = newStockFixture(t)
	ms := f.source
	ms.Entities = nil
	if _, err := f.store.SaveInventoryImport(ctx, ms, "overview-removed-store"); err != nil {
		t.Fatal(err)
	}
	page = readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25})
	if page.Counts.Mapped != 0 || page.Counts.Attention != 1 || page.Items[0].State != "missing_evidence" {
		t.Fatal("missing store still marked mapped")
	}
}
func TestWarehouseOverviewHiddenHistoryAndCandidates(t *testing.T) {
	f := newStockFixtureWithHistory(t, false)
	ctx := context.Background()
	oz := f.ozon
	oz.StartedAt = time.Now()
	oz.FinishedAt = oz.StartedAt
	ms := f.source
	ms.StartedAt = oz.StartedAt
	ms.FinishedAt = oz.FinishedAt
	if _, err := f.store.SaveOzonImport(ctx, oz, "overview-new-oz"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SaveInventoryImport(ctx, ms, "overview-new-ms"); err != nil {
		t.Fatal(err)
	}
	page := readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25})
	if page.Items[0].State != "conflict" || page.Items[0].MappingID != nil || page.Items[0].StoreID != "" || page.Items[0].StoreName != "" {
		t.Fatal("hidden history exposed or marked mapped")
	}
	candidates, err := f.store.WarehouseCandidates(ctx, overviewScope(f), postgres.WarehouseReadRequest{Limit: 25})
	if err != nil || len(candidates.Items) != 1 || candidates.Items[0].Available {
		t.Fatalf("occupied candidate accepted: %+v %v", candidates, err)
	}
}
func TestWarehouseOverviewTypesAndEmptyImport(t *testing.T) {
	f := newStockFixture(t)
	oz := f.ozon
	no, yes := false, true
	oz.Warehouses = []domain.OzonWarehouse{{ID: "unknown"}, {ID: "unsupported", IsRFBS: &yes, WarehouseType: "rfbs", Status: "disabled"}, {ID: "express", IsRFBS: &yes, WarehouseType: "rfbs_express", Status: "created"}, {ID: "fbs", IsRFBS: &no}}
	if _, err := f.store.SaveOzonImport(context.Background(), oz, "overview-types"); err != nil {
		t.Fatal(err)
	}
	page := readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25})
	states := make(map[string]string, len(page.Items))
	for _, v := range page.Items {
		states[v.ID] = v.State
	}
	if states["unknown"] != "missing_evidence" || states["unsupported"] != "unsupported" || states["express"] != "unmapped" || states["fbs"] != "unmapped" {
		t.Fatalf("types: %+v", states)
	}
	oz.Warehouses = nil
	oz.Stocks = nil
	if _, err := f.store.SaveOzonImport(context.Background(), oz, "overview-empty"); err != nil {
		t.Fatal(err)
	}
	page = readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25})
	if page.Counts.Mapped != 0 || page.Items[0].State != "missing_evidence" {
		t.Fatal("historical mapping has current evidence")
	}
}
func TestWarehouseStockSearchPrecisionAndUnknown(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	if _, err := f.store.Pool.Exec(ctx, `UPDATE source_warehouse_stock_observations SET stock=0.123456789012345678,quantity=NULL WHERE import_id IN(SELECT id FROM source_imports WHERE account_id=$1)`, f.req.Pool.MoyskladAccount); err != nil {
		t.Fatal(err)
	}
	result, err := f.store.WarehouseStock(ctx, f.req.Pool.Tenant, "moysklad", f.req.Pool.MoyskladAccount, "warehouse", "offer", 0)
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("stock read: %+v %v", result, err)
	}
	item := result.Items[0]
	if item.Quantity != nil || item.Present != nil || item.Stock == nil || *item.Stock != "0.123456789012345678" || item.Article != "offer" {
		t.Fatalf("precision/identity lost: %+v", item)
	}
	empty, err := f.store.WarehouseStock(ctx, f.req.Pool.Tenant, "moysklad", f.req.Pool.MoyskladAccount, "warehouse", "absent", 0)
	if err != nil || len(empty.Items) != 0 {
		t.Fatal("search ignored")
	}
	other, err := f.store.WarehouseStock(ctx, "other", "moysklad", f.req.Pool.MoyskladAccount, "warehouse", "", 0)
	if err != nil || len(other.Items) != 0 {
		t.Fatal("stock leaked across tenant")
	}
}

// Uses the established Vite/Playwright harness and local session fixture only.
func TestWarehouseOverviewBrowser(t *testing.T) {
	if os.Getenv("STYK_WAREHOUSE_BROWSER_E2E") != "1" {
		t.Skip("warehouse browser acceptance not requested")
	}
	f := newStockFixture(t)
	seedWarehouseBrowser(t, f)
	raw, err := json.Marshal(map[string]string{"tenant": f.req.Pool.Tenant, "moy": f.req.Pool.MoyskladAccount, "ozon": f.req.Target.OzonAccount})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STYK_WAREHOUSE_SCOPE", string(raw))
	handler, err := console.FixtureHandler(f.store, "", "http://127.0.0.1:15179")
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(warehouseBrowserAPI(t, f, handler))
	defer api.Close()
	runSettingsBrowser(t, api.URL, "e2e/warehouse-review.spec.ts", "e2e/warehouse-confirmation.spec.ts")
}

func seedWarehouseBrowser(t *testing.T, f stockFixtureData) {
	t.Helper()
	ctx := context.Background()
	ms, oz := f.source, f.ozon
	ms.Entities[0].Name = "Кофе арабика"
	ms.Entities[1].Name = "Основной склад"
	ms.Entities = append(ms.Entities, domain.ExternalEntity{Kind: "store", ID: "store-free", Name: "Склад Екатеринбург"}, domain.ExternalEntity{Kind: "store", ID: "store-read", Name: "Резервный склад"}, domain.ExternalEntity{Kind: "store", ID: "store-spare", Name: "Свободный склад"})
	zero := "0"
	ms.WarehouseStock = append(ms.WarehouseStock, domain.WarehouseStockObservation{Kind: "variant", ExternalID: "item", WarehouseID: "store-free", Stock: &zero, Reserve: &zero, InTransit: &zero})
	oz.Products[0].Name = "Кофе арабика"
	oz.Warehouses[0].Name = "Склад Москва"
	for i := 0; i < 54; i++ {
		oz.Warehouses = append(oz.Warehouses, domain.OzonWarehouse{ID: fmt.Sprintf("region-%03d", i), Name: fmt.Sprintf("Регион %03d", i), IsRFBS: oz.Warehouses[0].IsRFBS})
	}
	oz.Stocks = append(oz.Stocks, domain.OzonStock{ProductID: "product", OfferID: "offer", WarehouseID: "region-000", Present: 0, Reserved: 0, FreeStock: 0})
	yes := true
	oz.Warehouses = append(oz.Warehouses, domain.OzonWarehouse{ID: "unsupported", Name: "Недоступный rFBS", WarehouseType: "rfbs", Status: "disabled", IsRFBS: &yes}, domain.OzonWarehouse{ID: "unknown", Name: "Склад без типа"})
	if _, err := f.store.SaveInventoryImport(ctx, ms, "warehouse-browser-ms"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SaveOzonImport(ctx, oz, "warehouse-browser-oz"); err != nil {
		t.Fatal(err)
	}
}

func warehouseBrowserAPI(t *testing.T, f stockFixtureData, handler http.Handler) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__test/warehouse/conflict" && r.Method == http.MethodPost {
			if _, err := f.store.ConfirmWarehouseMapping(r.Context(), overviewScope(f), "store-read", "region-052", "concurrent browser fixture"); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/__test/warehouse/stale" && r.Method == http.MethodPost {
			_, err := f.store.Pool.Exec(r.Context(), `UPDATE source_imports SET fingerprint=fingerprint||'-stale' WHERE id=(SELECT id FROM source_imports WHERE source='ozon' AND account_id=$1 ORDER BY id DESC LIMIT 1)`, f.req.Target.OzonAccount)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(204)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func TestWarehouseOverviewHTTPParametersAndAccess(t *testing.T) {
	f := newStockFixture(t)
	handler, err := console.FixtureHandler(f.store, "", "http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest("POST", "http://127.0.0.1/api/session", strings.NewReader(`{"fixture":true}`))
	login.Header.Set("Origin", "http://127.0.0.1:5173")
	login.Header.Set("Content-Type", "application/json")
	login.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, login)
	if response.Code != 204 {
		t.Fatalf("fixture login: %d", response.Code)
	}
	cookie := response.Result().Cookies()[0]
	query := url.Values{"view": {"overview"}, "tenant": {f.req.Pool.Tenant}, "moysklad_account": {f.req.Pool.MoyskladAccount}, "ozon_account": {f.req.Target.OzonAccount}}
	tests := []struct {
		name, key, value string
		code             int
	}{{"valid", "view", "overview", 200}, {"candidates", "view", "candidates", 200}, {"invalid view", "view", "other", 400}, {"negative page", "offset", "-1", 400}, {"oversized page", "offset", "100001", 400}, {"fraction", "offset", "0.5", 400}, {"invalid filter", "filter", "not-a-filter", 400}, {"long search", "search", strings.Repeat("x", 257), 400}, {"cross tenant", "tenant", "foreign", 404}, {"cross account", "ozon_account", "foreign", 404}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{}
			for key, values := range query {
				q[key] = append([]string(nil), values...)
			}
			q.Set(tc.key, tc.value)
			request := httptest.NewRequest("GET", "http://127.0.0.1/api/warehouses?"+q.Encode(), nil)
			request.RemoteAddr = "127.0.0.1:1234"
			request.AddCookie(cookie)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.code {
				t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestWarehouseOverviewEmptyCompletedImports(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	scope := overviewScope(f)
	scope.Tenant += "-empty"
	scope.MoyskladAccount += "-empty"
	scope.OzonAccount += "-empty"
	now := time.Now()
	if _, err := f.store.SaveInventoryImport(ctx, domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now}, "empty"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SaveOzonImport(ctx, domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now}, "empty"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ source, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err := f.store.RegisterStockConnection(ctx, scope.Tenant, c.source, c.account, "empty fixture", true); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.store.WarehouseOverview(ctx, scope, postgres.WarehouseReadRequest{Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.Counts.Total != 0 || page.HasNext || !page.OzonImported || !page.MoyImported {
		t.Fatalf("empty completed imports: %+v", page)
	}
}

func TestWarehouseStockStablePagesAndFutureObservation(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	oz := f.ozon
	for i := 0; i < 56; i++ {
		id := fmt.Sprintf("late-%03d", i)
		oz.Products = append(oz.Products, domain.OzonProduct{ProductID: id, OfferID: id, Name: "Товар " + id})
		oz.Stocks = append(oz.Stocks, domain.OzonStock{ProductID: id, OfferID: id, WarehouseID: f.req.Target.WarehouseID})
	}
	if _, err := f.store.SaveOzonImport(ctx, oz, "stock-pages"); err != nil {
		t.Fatal(err)
	}
	scope := overviewScope(f)
	first, err := f.store.WarehouseStock(ctx, scope.Tenant, "ozon", scope.OzonAccount, f.req.Target.WarehouseID, "", 0)
	if err != nil || len(first.Items) != 50 || !first.HasNext {
		t.Fatalf("first stock page: %v %+v", err, first)
	}
	last, err := f.store.WarehouseStock(ctx, scope.Tenant, "ozon", scope.OzonAccount, f.req.Target.WarehouseID, "", 50)
	if err != nil || len(last.Items) != 7 || last.HasNext || last.Items[0].ItemID <= first.Items[49].ItemID {
		t.Fatalf("last stock page: %v %+v", err, last)
	}
	search, err := f.store.WarehouseStock(ctx, scope.Tenant, "ozon", scope.OzonAccount, f.req.Target.WarehouseID, "Товар late-055", 0)
	if err != nil || len(search.Items) != 1 || search.Items[0].Present == nil || *search.Items[0].Present != "0" || search.Items[0].Stock != nil {
		t.Fatalf("source stock search: %v %+v", err, search)
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE ozon_fbs_stock_observations SET observed_at=now()+interval '1 hour' WHERE import_id=(SELECT id FROM source_imports WHERE source='ozon' AND account_id=$1 ORDER BY id DESC LIMIT 1) AND product_id='product'`, scope.OzonAccount); err != nil {
		t.Fatal(err)
	}
	overview := readOverview(t, f, postgres.WarehouseReadRequest{Limit: 25})
	if overview.Items[0].ObservedAt == nil || !overview.Items[0].ObservedAt.After(time.Now()) {
		t.Fatal("future observation was hidden by older rows")
	}
}
