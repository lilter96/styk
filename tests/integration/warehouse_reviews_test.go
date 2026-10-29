//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

func TestDurableWarehouseReviewLifecycle(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	in := domain.WarehouseReviewRequest{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount, MoyskladWarehouseID: f.req.Pool.WarehouseID, OzonWarehouseID: f.req.Target.WarehouseID, SessionID: "synthetic-session", Lifetime: time.Minute}
	var tasksBefore int
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_tasks`).Scan(&tasksBefore); err != nil {
		t.Fatal(err)
	}
	result, err := f.store.CreateWarehouseReview(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "CURRENT" || result.Review.ExistingMappingVersion == nil || result.Review.MoyskladImportID == 0 || result.Review.OzonImportID == 0 {
		t.Fatal("invalid captured review")
	}
	if _, err = f.store.InspectWarehouseReview(ctx, result.Review.ID, "other-session"); !errors.Is(err, postgres.ErrReviewMissing) {
		t.Fatal("wrong session allowed")
	}
	got, err := f.store.InspectWarehouseReview(ctx, result.Review.ID, in.SessionID)
	if err != nil || got.Status != "CURRENT" {
		t.Fatalf("current review: %v %s", err, got.Status)
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE warehouse_mapping_reviews SET session_audit_id='altered' WHERE id=$1`, result.Review.ID); err == nil {
		t.Fatal("review mutated")
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE source_imports SET fingerprint='changed' WHERE id=$1`, result.Review.OzonImportID); err != nil {
		t.Fatal(err)
	}
	got, err = f.store.InspectWarehouseReview(ctx, result.Review.ID, in.SessionID)
	if err != nil || got.Status != "STALE_SOURCE" {
		t.Fatalf("fingerprint change: %v %s", err, got.Status)
	}
	var maps, hist, reviews int
	if err = f.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1),(SELECT count(*) FROM warehouse_mapping_history WHERE tenant_id=$1),(SELECT count(*) FROM warehouse_mapping_reviews WHERE tenant_id=$1)`, in.Tenant).Scan(&maps, &hist, &reviews); err != nil || maps != 1 || hist != 1 || reviews != 1 {
		t.Fatal("review changed mapping or audit")
	}
	var tasksAfter, decisions int
	if err = f.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbox_tasks),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1)`, in.Tenant).Scan(&tasksAfter, &decisions); err != nil || tasksBefore != tasksAfter || decisions != 0 {
		t.Fatal("review created a task or stock decision")
	}
}
func TestDurableWarehouseReviewRejectsInvalidAndConflictingPairs(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	in := domain.WarehouseReviewRequest{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount, MoyskladWarehouseID: f.req.Pool.WarehouseID, OzonWarehouseID: f.req.Target.WarehouseID, SessionID: "s", Lifetime: time.Minute}
	bad := in
	bad.Tenant += "other"
	if _, err := f.store.CreateWarehouseReview(ctx, bad); !errors.Is(err, postgres.ErrReviewScope) {
		t.Fatal("cross-tenant accepted")
	}
	bad = in
	bad.MoyskladWarehouseID = "missing"
	if _, err := f.store.CreateWarehouseReview(ctx, bad); !errors.Is(err, postgres.ErrReviewSource) {
		t.Fatal("missing warehouse accepted")
	}
	bad = in
	bad.OzonWarehouseID = "missing"
	if _, err := f.store.CreateWarehouseReview(ctx, bad); !errors.Is(err, postgres.ErrReviewSource) {
		t.Fatal("missing Ozon warehouse accepted")
	}
	bad = in
	bad.OzonAccount += "-other"
	if _, err := f.store.CreateWarehouseReview(ctx, bad); !errors.Is(err, postgres.ErrReviewScope) {
		t.Fatal("unregistered account accepted")
	}
	if _, err := f.store.Pool.Exec(ctx, `UPDATE ozon_warehouse_observations SET is_rfbs=true WHERE warehouse_id=$1 AND import_id=(SELECT max(id) FROM source_imports WHERE account_id=$2)`, in.OzonWarehouseID, in.OzonAccount); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateWarehouseReview(ctx, in); !errors.Is(err, postgres.ErrReviewEvidence) {
		t.Fatal("rFBS accepted")
	}
}
func TestWarehouseReviewHTTPRequiresSessionAndOrigin(t *testing.T) {
	f := newStockFixture(t)
	secret := strings.Repeat("x", 32)
	h, err := console.Handler(f.store, secret, "http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"tenant":%q,"moysklad_account":%q,"ozon_account":%q,"moysklad_warehouse_id":%q,"ozon_warehouse_id":%q}`, f.req.Pool.Tenant, f.req.Pool.MoyskladAccount, f.req.Target.OzonAccount, f.req.Pool.WarehouseID, f.req.Target.WarehouseID)
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/warehouse-mapping-reviews", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Origin", "http://127.0.0.1:5173")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated review")
	}
	login := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/session", strings.NewReader(fmt.Sprintf(`{"secret":%q}`, secret)))
	login.RemoteAddr = "127.0.0.1:1234"
	login.Header.Set("Origin", "http://127.0.0.1:5173")
	login.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, login)
	cookie := w.Result().Cookies()[0]
	req = httptest.NewRequest("POST", "http://127.0.0.1:8080/api/warehouse-mapping-reviews", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Origin", "http://invalid")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatal("invalid origin accepted")
	}
	post := func(payload string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/warehouse-mapping-reviews", strings.NewReader(payload))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://127.0.0.1:5173")
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(cookie)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		return out
	}
	if out := post(strings.TrimSuffix(body, "}") + `,"session_audit_id":"fake"}`); out.Code != 400 {
		t.Fatal("browser principal accepted")
	}
	out := post(body)
	if out.Code != 201 {
		t.Fatalf("review creation: %d", out.Code)
	}
	var result domain.WarehouseReviewResult
	if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil || result.Status != "CURRENT" {
		t.Fatal("review response")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/warehouse-mapping-reviews/"+result.Review.ID, nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.AddCookie(cookie)
	out = httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"status":"CURRENT"`) {
		t.Fatal("review inspection")
	}
	login = httptest.NewRequest("POST", "http://127.0.0.1:8080/api/session", strings.NewReader(fmt.Sprintf(`{"secret":%q}`, secret)))
	login.RemoteAddr = "127.0.0.1:1234"
	login.Header.Set("Origin", "http://127.0.0.1:5173")
	login.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, login)
	r = httptest.NewRequest("GET", "http://127.0.0.1:8080/api/warehouse-mapping-reviews/"+result.Review.ID, nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.AddCookie(w.Result().Cookies()[0])
	out = httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 404 {
		t.Fatal("another session inspected review")
	}
}

func TestWarehouseReviewStalenessAndExpiry(t *testing.T) {
	for _, scenario := range []string{"moy-import", "ozon-import", "warehouse-name", "classification", "mapping", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			f := newStockFixture(t)
			ctx := context.Background()
			in := domain.WarehouseReviewRequest{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount, MoyskladWarehouseID: f.req.Pool.WarehouseID, OzonWarehouseID: f.req.Target.WarehouseID, SessionID: "test-session", Lifetime: time.Minute}
			if scenario == "expired" {
				in.Lifetime = 3 * time.Millisecond
			}
			result, err := f.store.CreateWarehouseReview(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "moy-import":
				ms := f.source
				ms.StartedAt = time.Now()
				ms.FinishedAt = ms.StartedAt
				if _, err = f.store.SaveInventoryImport(ctx, ms, "warehouse-new-ms"); err != nil {
					t.Fatal(err)
				}
			case "ozon-import":
				oz := f.ozon
				oz.StartedAt = time.Now()
				oz.FinishedAt = oz.StartedAt
				if _, err = f.store.SaveOzonImport(ctx, oz, "warehouse-new-oz"); err != nil {
					t.Fatal(err)
				}
			case "classification":
				if _, err = f.store.Pool.Exec(ctx, `UPDATE ozon_warehouse_observations SET is_rfbs=true WHERE import_id=$1 AND warehouse_id=$2`, result.Review.OzonImportID, in.OzonWarehouseID); err != nil {
					t.Fatal(err)
				}
			case "warehouse-name":
				if _, err = f.store.Pool.Exec(ctx, `UPDATE source_entities SET name='changed' WHERE import_id=$1 AND kind='store' AND external_id=$2`, result.Review.MoyskladImportID, in.MoyskladWarehouseID); err != nil {
					t.Fatal(err)
				}
			case "mapping":
				if _, err = f.store.Pool.Exec(ctx, `UPDATE warehouse_mappings SET version=2 WHERE id=$1`, result.Review.ExistingMappingID); err == nil {
					t.Fatal("unsupported mapping version accepted")
				}
				if _, err = f.store.Pool.Exec(ctx, `UPDATE warehouse_mappings SET provenance='new synthetic provenance' WHERE id=$1`, result.Review.ExistingMappingID); err != nil {
					t.Fatal(err)
				}
			case "expired":
				time.Sleep(10 * time.Millisecond)
			}
			got, err := f.store.InspectWarehouseReview(ctx, result.Review.ID, in.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			expected := "STALE_SOURCE"
			if scenario == "mapping" {
				expected = "STALE_MAPPING"
			}
			if scenario == "expired" {
				expected = "EXPIRED"
			}
			if got.Status != expected {
				t.Fatalf("got %s, want %s", got.Status, expected)
			}
		})
	}
}
func TestWarehouseReviewRejectsHiddenOrConflictingMapping(t *testing.T) {
	f := newStockFixtureWithHistory(t, false)
	ctx := context.Background()
	in := domain.WarehouseReviewRequest{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount, MoyskladWarehouseID: f.req.Pool.WarehouseID, OzonWarehouseID: f.req.Target.WarehouseID, SessionID: "s", Lifetime: time.Minute}
	ms := f.source
	ms.StartedAt = time.Now()
	ms.FinishedAt = ms.StartedAt
	ms.Entities = append(ms.Entities, domain.ExternalEntity{Kind: "store", ID: "other-warehouse"})
	if _, err := f.store.SaveInventoryImport(ctx, ms, "warehouse-visible-moy"); err != nil {
		t.Fatal(err)
	}
	oz := f.ozon
	oz.StartedAt = time.Now()
	oz.FinishedAt = oz.StartedAt
	if _, err := f.store.SaveOzonImport(ctx, oz, "warehouse-visible-ozon"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateWarehouseReview(ctx, in); !errors.Is(err, postgres.ErrReviewConflict) {
		t.Fatal("hidden mapping must block")
	}
	in.MoyskladWarehouseID = "other-warehouse"
	if _, err := f.store.CreateWarehouseReview(ctx, in); !errors.Is(err, postgres.ErrReviewConflict) {
		t.Fatal("conflicting mapping must block")
	}
}

func TestConcurrentImportAndWarehouseReviewCaptureConsistentRevision(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	in := domain.WarehouseReviewRequest{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount, MoyskladWarehouseID: f.req.Pool.WarehouseID, OzonWarehouseID: f.req.Target.WarehouseID, SessionID: "concurrent", Lifetime: time.Minute}
	type created struct {
		result domain.WarehouseReviewResult
		err    error
	}
	ch := make(chan created, 1)
	imports := make(chan error, 1)
	start := make(chan struct{})
	go func() { <-start; r, e := f.store.CreateWarehouseReview(ctx, in); ch <- created{r, e} }()
	go func() {
		<-start
		oz := f.ozon
		oz.StartedAt = time.Now()
		oz.FinishedAt = oz.StartedAt
		_, e := f.store.SaveOzonImport(ctx, oz, "concurrent-warehouse-review-import")
		imports <- e
	}()
	close(start)
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	if err := <-imports; err != nil {
		t.Fatal(err)
	}
	var matching bool
	err := f.store.Pool.QueryRow(ctx, `SELECT r.ozon_import_fingerprint=i.fingerprint FROM warehouse_mapping_reviews r JOIN source_imports i ON i.id=r.ozon_import_id WHERE r.id=$1`, r.result.Review.ID).Scan(&matching)
	if err != nil || !matching {
		t.Fatal("review captured inconsistent import revision")
	}
	inspected, err := f.store.InspectWarehouseReview(ctx, r.result.Review.ID, in.SessionID)
	if err != nil || (inspected.Status != "CURRENT" && inspected.Status != "STALE_SOURCE") {
		t.Fatalf("unexpected concurrent status: %s %v", inspected.Status, err)
	}
}

func TestWarehouseReviewForUnmappedPair(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	now := time.Now()
	ms := f.source
	ms.StartedAt = now
	ms.FinishedAt = now
	ms.Entities = append(ms.Entities, domain.ExternalEntity{Kind: "store", ID: "new-moy", Name: "Synthetic"})
	if _, err := f.store.SaveInventoryImport(ctx, ms, "new-warehouse-ms"); err != nil {
		t.Fatal(err)
	}
	oz := f.ozon
	oz.StartedAt = now
	oz.FinishedAt = now
	rfbs := false
	oz.Warehouses = append(oz.Warehouses, domain.OzonWarehouse{ID: "new-ozon", Name: "Synthetic", IsRFBS: &rfbs})
	if _, err := f.store.SaveOzonImport(ctx, oz, "new-warehouse-oz"); err != nil {
		t.Fatal(err)
	}
	in := domain.WarehouseReviewRequest{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount, MoyskladWarehouseID: "new-moy", OzonWarehouseID: "new-ozon", SessionID: "unmapped", Lifetime: time.Minute}
	r, err := f.store.CreateWarehouseReview(ctx, in)
	if err != nil || r.Status != "CURRENT" || r.Review.ExistingMappingID != nil || r.Review.MoyskladWarehouseID != "new-moy" || r.Review.OzonWarehouseID != "new-ozon" {
		t.Fatalf("unmapped review failed: %v", err)
	}
	got, err := f.store.InspectWarehouseReview(ctx, r.Review.ID, in.SessionID)
	if err != nil || got.Status != "CURRENT" {
		t.Fatal("unmapped review inspection failed")
	}
}
