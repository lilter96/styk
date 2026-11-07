//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/matching"
	"github.com/lilter96/styk/internal/postgres"
)

func TestProductWorkspaceIncludesBothSourcesAndMissingConfirmedObservation(t *testing.T) {
	db, scope := productMatchingFixture(t)
	h, cookie := catalogHandler(t, db.Pool)
	q := url.Values{"tenant": {scope.Tenant}, "moysklad_account": {scope.MoyskladAccount}, "ozon_account": {scope.OzonAccount}, "limit": {"20"}}
	initial := workspaceProducts(t, h, cookie, q)
	if initial.Total != 6 || len(initial.Items) != 6 {
		t.Fatalf("unified catalog omitted or duplicated source identities: %+v", initial)
	}
	job, err := db.CreateProductMatchingJob(context.Background(), scope, "matching-test", "read-model-search", matching.ArticleOrCode)
	if err != nil {
		t.Fatal(err)
	}
	app, err := db.ApplyProductMatchingJob(context.Background(), postgres.ProductMatchingApproval{Scope: scope, JobID: job.ID, Actor: "matching-test", Key: "read-model-link", Expected: 1, Acknowledge: true})
	if err != nil || app.State != "COMPLETED" || app.Confirmed != 1 {
		t.Fatalf("read-model fixture: %+v %v", app, err)
	}
	now := time.Now().UTC()
	active := false
	_, err = db.SaveOzonImport(context.Background(), domain.OzonImport{Account: scope.OzonAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "102", OfferID: "B", Name: "Чашка красная", Archived: &active}}}, "missing-101")
	if err != nil {
		t.Fatal(err)
	}
	result := workspaceProducts(t, h, cookie, q)
	if result.Total != 4 {
		t.Fatalf("missing observation lost confirmed link or duplicated mapped target: %+v", result)
	}
	for _, item := range result.Items {
		if item.ID == "101" && item.Provider == "ozon" && !item.Observed && item.Status == "CONFIRMED" {
			return
		}
	}
	t.Fatal("confirmed unobserved source unavailable")
}

type workspaceProductPage struct {
	Total int `json:"total"`
	Items []struct {
		ID       string `json:"product_id"`
		Provider string `json:"provider"`
		Observed bool   `json:"observed"`
		Status   string `json:"match_status"`
	} `json:"items"`
}

func workspaceProducts(t *testing.T, h http.Handler, cookie *http.Cookie, q url.Values) workspaceProductPage {
	t.Helper()
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/products?"+q.Encode(), nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var value workspaceProductPage
	if w.Code != 200 {
		t.Fatalf("products HTTP %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCatalogImportSummaryCountsWholePinnedCatalog(t *testing.T) {
	db, scope := productMatchingFixture(t)
	request := catalogsync.PageRequest{Provider: "ozon", Limit: 1}
	first, err := db.CatalogProducts(context.Background(), scope, request)
	if err != nil || len(first.Products) != 1 || first.Summary == nil || first.Summary.Total != 3 || first.Summary.ToReview != 3 || first.NextCursor == "" {
		t.Fatalf("first summary %+v: %v", first, err)
	}
	request.Cursor = first.NextCursor
	second, err := db.CatalogProducts(context.Background(), scope, request)
	if err != nil || second.Summary.Total != 3 || second.Products[0].Identity.ID == first.Products[0].Identity.ID {
		t.Fatalf("summary counted a page: %+v %v", second, err)
	}
}
