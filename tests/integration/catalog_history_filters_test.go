//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

func TestCatalogHistoryFiltersKeepScope(t *testing.T) {
	db, config := catalogSyncFixture(t)
	operation := catalogSyncApprove(t, db, catalogSyncPreview(t, db, config))
	tests := []struct {
		name    string
		request catalogsync.PageRequest
		want    int
	}{
		{"source name", catalogsync.PageRequest{Limit: 1, Search: "New name"}, 1},
		{"case insensitive", catalogsync.PageRequest{Limit: 1, Search: "NEW NAME"}, 1},
		{"offer", catalogsync.PageRequest{Limit: 1, Search: "shared-1"}, 1},
		{"operation", catalogsync.PageRequest{Limit: 1, Search: operation.ID}, 1},
		{"active", catalogsync.PageRequest{Limit: 1, State: "active"}, 1},
		{"reconciled", catalogsync.PageRequest{Limit: 1, State: "RECONCILED"}, 0},
		{"source identity", catalogsync.PageRequest{Limit: 1, ProductProvider: catalog.ProviderMoySklad, ProductID: "source-1"}, 1},
		{"target identity", catalogsync.PageRequest{Limit: 1, ProductProvider: catalog.ProviderOzon, ProductID: "1001"}, 1},
		{"other identity", catalogsync.PageRequest{Limit: 1, ProductProvider: catalog.ProviderOzon, ProductID: "1002"}, 0},
		{"literal wildcard", catalogsync.PageRequest{Limit: 1, Search: "%"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := db.CatalogHistory(context.Background(), config.Scope, tc.request)
			if err != nil || len(page.Operations) != tc.want {
				t.Fatalf("history: %+v %v", page, err)
			}
			if tc.want > 0 && (page.Operations[0].ID != operation.ID || page.Summary["QUEUED"] != 1) {
				t.Fatalf("wrong history/summary: %+v", page)
			}
		})
	}
	foreign := config.Scope
	foreign.Tenant = "another-workspace"
	page, err := db.CatalogHistory(context.Background(), foreign, catalogsync.PageRequest{Limit: 10})
	if err != nil || len(page.Operations) != 0 || len(page.Summary) != 0 {
		t.Fatalf("cross-tenant history: %+v %v", page, err)
	}
}

func TestProductConflictFilterDoesNotExposeForeignTarget(t *testing.T) {
	f := newCatalogPerformanceFixture(t, 6)
	seedOtherMappingAccount(t, f)
	request := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/products?tenant=tenant&ozon_account=ozon&moysklad_account=moy&mapping=conflict", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.AddCookie(f.cookie)
	recorder := httptest.NewRecorder()
	f.h.ServeHTTP(recorder, request)
	var page catalogPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || len(page.Items) != 1 || page.Items[0].ProductID != "product-000004" || page.Items[0].Status != "CONFLICT" {
		t.Fatalf("conflict page: %d %+v", recorder.Code, page)
	}
	if page.Items[0].TargetName != nil || page.Items[0].TargetArticle != nil {
		t.Fatal("foreign target exposed")
	}
}
