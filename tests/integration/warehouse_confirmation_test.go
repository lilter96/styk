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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/commitment"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

type warehouseConfirmationHarness struct{ *confirmationHarness }

func newWarehouseConfirmationHarness(t *testing.T) *warehouseConfirmationHarness {
	return newWarehouseConfirmationHarnessWithHistory(t, true)
}

func newWarehouseConfirmationHarnessWithHistory(t *testing.T, includeHistory bool) *warehouseConfirmationHarness {
	t.Helper()
	f := newStockFixtureWithHistory(t, includeHistory)
	now := time.Now().UTC()
	f.source.Entities = append(f.source.Entities,
		domain.ExternalEntity{Kind: "store", ID: "warehouse-a", Name: "Synthetic source A"},
		domain.ExternalEntity{Kind: "store", ID: "warehouse-b", Name: "Synthetic source B"},
	)
	stock, reserve, incoming := "10", "1", "0"
	f.source.WarehouseStock = append(f.source.WarehouseStock, domain.WarehouseStockObservation{Kind: "variant", ExternalID: "item", WarehouseID: "warehouse-a", Stock: &stock, Reserve: &reserve, InTransit: &incoming})
	f.source.StartedAt, f.source.FinishedAt = now, now
	if _, err := f.store.SaveInventoryImport(context.Background(), f.source, "warehouse-confirm-moy"); err != nil {
		t.Fatal(err)
	}
	fbs, rfbs := false, true
	f.ozon.Warehouses = append(f.ozon.Warehouses,
		domain.OzonWarehouse{ID: "ozon-a", Name: "Synthetic FBS A", IsRFBS: &fbs},
		domain.OzonWarehouse{ID: "ozon-b", Name: "Synthetic FBS B", IsRFBS: &fbs},
		domain.OzonWarehouse{ID: "ozon-rfbs", Name: "Synthetic rFBS", IsRFBS: &rfbs},
		domain.OzonWarehouse{ID: "ozon-unknown", Name: "Synthetic unknown"},
	)
	f.ozon.Stocks = append(f.ozon.Stocks, domain.OzonStock{ProductID: "product", OfferID: "offer", SKU: "sku", WarehouseID: "ozon-a", Present: 5, Reserved: 1, FreeStock: 4})
	f.ozon.StartedAt, f.ozon.FinishedAt = now, now
	if _, err := f.store.SaveOzonImport(context.Background(), f.ozon, "warehouse-confirm-ozon"); err != nil {
		t.Fatal(err)
	}
	handler, err := console.Handler(f.store, confirmationTestSecret, confirmationTestOrigin)
	if err != nil {
		t.Fatal(err)
	}
	h := &confirmationHarness{f: f, h: handler}
	h.cookie = h.login(t)
	return &warehouseConfirmationHarness{h}
}

func (h *warehouseConfirmationHarness) warehouseReviewBody(moy, ozon string) string {
	return fmt.Sprintf(`{"tenant":%q,"moysklad_account":%q,"ozon_account":%q,"moysklad_warehouse_id":%q,"ozon_warehouse_id":%q}`, h.f.req.Pool.Tenant, h.f.req.Pool.MoyskladAccount, h.f.req.Target.OzonAccount, moy, ozon)
}
func (h *warehouseConfirmationHarness) createWarehouseReview(t *testing.T, moy, ozon string) domain.WarehouseReviewResult {
	t.Helper()
	w := h.post("/api/warehouse-mapping-reviews", h.warehouseReviewBody(moy, ozon), h.cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("create warehouse review: %d %s", w.Code, w.Body.String())
	}
	var result domain.WarehouseReviewResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "CURRENT" {
		t.Fatalf("unexpected review status: %s", result.Status)
	}
	return result
}
func warehouseConfirmationPath(id string) string {
	return "/api/warehouse-mapping-reviews/" + id + "/confirm"
}
func (h *warehouseConfirmationHarness) assertWarehouseRecords(t *testing.T, moy string, wantMapping, wantHistory, wantAudit, wantOutcome int) {
	t.Helper()
	var m, history, audit, outcome int
	err := h.f.store.Pool.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND moysklad_warehouse_id=$4),
 (SELECT count(*) FROM warehouse_mapping_history WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND moysklad_warehouse_id=$4),
 (SELECT count(*) FROM warehouse_mapping_confirmation_audits WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND moysklad_warehouse_id=$4),
 (SELECT count(*) FROM warehouse_mapping_confirmation_idempotency i JOIN warehouse_mapping_confirmation_audits a ON a.mapping_id=i.mapping_id AND a.mapping_version=i.mapping_version WHERE a.tenant_id=$1 AND a.moysklad_account=$2 AND a.ozon_account=$3 AND a.moysklad_warehouse_id=$4)`, h.f.req.Pool.Tenant, h.f.req.Pool.MoyskladAccount, h.f.req.Target.OzonAccount, moy).Scan(&m, &history, &audit, &outcome)
	if err != nil {
		t.Fatal(err)
	}
	if m != wantMapping || history != wantHistory || audit != wantAudit || outcome != wantOutcome {
		t.Fatalf("mapping/history/audit/outcome: %d/%d/%d/%d", m, history, audit, outcome)
	}
}
func TestWarehouseConfirmationSuccessAndLostResponseReplay(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	var beforePools, beforeDecisions, beforeTasks int
	err := h.f.store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM stock_pools WHERE tenant_id=$1),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1),(SELECT count(*) FROM outbox_tasks)`, h.f.req.Pool.Tenant).Scan(&beforePools, &beforeDecisions, &beforeTasks)
	if err != nil {
		t.Fatal(err)
	}
	body := confirmationBody("wh-synthetic-key", "Reviewed warehouse pair")
	first := parseConfirmation(t, h.post(warehouseConfirmationPath(review.Review.ID), body, h.cookie))
	if first.MappingID <= 0 || first.MappingVersion != 1 || first.ReviewID != review.Review.ID || first.EvidenceClass != domain.MappingOperatorAttested || first.ConfirmedAt.IsZero() || first.IdempotentReplay {
		t.Fatal("incorrect confirmation result")
	}
	second := parseConfirmation(t, h.post(warehouseConfirmationPath(review.Review.ID), body, h.cookie))
	if !second.IdempotentReplay || second.MappingID != first.MappingID || second.MappingVersion != first.MappingVersion || !second.ConfirmedAt.Equal(first.ConfirmedAt) {
		t.Fatal("lost-response replay changed the result")
	}
	if w := h.post(warehouseConfirmationPath(review.Review.ID), confirmationBody("wh-synthetic-key", "different"), h.cookie); w.Code != 409 || !strings.Contains(w.Body.String(), `"IDEMPOTENCY_CONFLICT"`) {
		t.Fatal("key collision not rejected")
	}
	if w := h.post(warehouseConfirmationPath(review.Review.ID), `{"idempotency_key":"wh-synthetic-key","acknowledge":false,"explanation":"Reviewed warehouse pair"}`, h.cookie); w.Code != 409 || !strings.Contains(w.Body.String(), `"IDEMPOTENCY_CONFLICT"`) {
		t.Fatal("changed acknowledgement did not conflict")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
	var tenant, moyAcc, ozAcc, moyID, ozID, principal, evidence, session, explanation, provenance, reviewID string
	var moyImport, ozonImport int64
	var confirmed time.Time
	err = h.f.store.Pool.QueryRow(context.Background(), `SELECT a.tenant_id,a.moysklad_account,a.ozon_account,a.moysklad_warehouse_id,a.ozon_warehouse_id,a.principal_type,a.evidence_class,a.session_audit_id,a.explanation,m.provenance,a.review_id,a.moysklad_import_id,a.ozon_import_id,a.confirmed_at FROM warehouse_mapping_confirmation_audits a JOIN warehouse_mappings m ON m.id=a.mapping_id WHERE a.mapping_id=$1`, first.MappingID).Scan(&tenant, &moyAcc, &ozAcc, &moyID, &ozID, &principal, &evidence, &session, &explanation, &provenance, &reviewID, &moyImport, &ozonImport, &confirmed)
	if err != nil {
		t.Fatal(err)
	}
	var storedSession string
	if err = h.f.store.Pool.QueryRow(context.Background(), `SELECT session_audit_id FROM warehouse_mapping_reviews WHERE id=$1`, review.Review.ID).Scan(&storedSession); err != nil {
		t.Fatal(err)
	}
	if tenant != h.f.req.Pool.Tenant || moyAcc != h.f.req.Pool.MoyskladAccount || ozAcc != h.f.req.Target.OzonAccount || moyID != "warehouse-a" || ozID != "ozon-a" || principal != "LOCAL_OPERATOR_SESSION" || evidence != "OPERATOR_ATTESTED" || session != storedSession || session == h.cookie.Value || explanation != "Reviewed warehouse pair" || provenance != "LOCAL_OPERATOR_SESSION" || reviewID != review.Review.ID || moyImport != review.Review.MoyskladImportID || ozonImport != review.Review.OzonImportID || !confirmed.Equal(first.ConfirmedAt) {
		t.Fatal("audit does not match review and result")
	}
	if strings.Contains(h.post(warehouseConfirmationPath(review.Review.ID), body, h.cookie).Body.String(), session) {
		t.Fatal("session audit identity leaked")
	}
	var pools, decisions, tasks int
	if err = h.f.store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM stock_pools WHERE tenant_id=$1),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1),(SELECT count(*) FROM outbox_tasks)`, h.f.req.Pool.Tenant).Scan(&pools, &decisions, &tasks); err != nil || pools != beforePools || decisions != beforeDecisions || tasks != beforeTasks {
		t.Fatal("confirmation created stock state or background task")
	}
	next := h.f.ozon
	next.StartedAt, next.FinishedAt = time.Now(), time.Now()
	if _, err = h.f.store.SaveOzonImport(context.Background(), next, "warehouse-confirm-after-success"); err != nil {
		t.Fatal(err)
	}
	afterImport := parseConfirmation(t, h.post(warehouseConfirmationPath(review.Review.ID), body, h.cookie))
	if !afterImport.IdempotentReplay || afterImport.MappingID != first.MappingID {
		t.Fatal("later import hid the committed outcome")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
}

func TestWarehouseConfirmationAuthenticationAndRequestScope(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	path := warehouseConfirmationPath(review.Review.ID)
	valid := confirmationBody("security-key", "synthetic")
	otherSession := h.login(t)
	cases := []struct {
		name, body, origin, host, remote string
		cookie                           *http.Cookie
		status                           int
		code                             string
	}{
		{"missing session", valid, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", nil, 401, "UNAUTHORIZED"},
		{"invalid session", valid, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", &http.Cookie{Name: h.cookie.Name, Value: "invalid"}, 401, "UNAUTHORIZED"},
		{"different session", valid, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", otherSession, 404, "REVIEW_NOT_FOUND"},
		{"invalid origin", valid, "http://example.invalid", "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 403, "FORBIDDEN"},
		{"missing origin", valid, "", "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 403, "FORBIDDEN"},
		{"invalid host", valid, confirmationTestOrigin, "example.invalid:8080", "127.0.0.1:1234", h.cookie, 403, "FORBIDDEN"},
		{"remote peer", valid, confirmationTestOrigin, "127.0.0.1:8080", "192.0.2.1:2222", h.cookie, 403, "FORBIDDEN"},
		{"missing acknowledgement", `{"idempotency_key":"security-key"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"negative acknowledgement", `{"idempotency_key":"security-key","acknowledge":false}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake principal", `{"idempotency_key":"security-key","acknowledge":true,"principal":"human"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake session", `{"idempotency_key":"security-key","acknowledge":true,"session_audit_id":"fake"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake tenant", `{"idempotency_key":"security-key","acknowledge":true,"tenant":"other"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake account", `{"idempotency_key":"security-key","acknowledge":true,"ozon_account":"other"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake import", `{"idempotency_key":"security-key","acknowledge":true,"ozon_import_id":3}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"fake evidence", `{"idempotency_key":"security-key","acknowledge":true,"evidence_class":"INDEPENDENTLY_VERIFIED"}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"invalid key", `{"idempotency_key":"bad key","acknowledge":true}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"oversize", fmt.Sprintf(`{"idempotency_key":"security-key","acknowledge":true,"explanation":%q}`, strings.Repeat("x", 5000)), confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
		{"multiple JSON", valid + ` {}`, confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie, 400, "INVALID_REQUEST"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := h.call("POST", path, tc.body, tc.origin, tc.host, tc.remote, tc.cookie)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"`+tc.code+`"`) {
				t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), confirmationTestSecret) || strings.Contains(w.Body.String(), h.cookie.Value) {
				t.Fatal("credential exposed")
			}
		})
	}
	h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
	for _, body := range []string{
		strings.Replace(h.warehouseReviewBody("warehouse-a", "ozon-a"), h.f.req.Pool.Tenant, h.f.req.Pool.Tenant+"-other", 1),
		strings.Replace(h.warehouseReviewBody("warehouse-a", "ozon-a"), h.f.req.Target.OzonAccount, h.f.req.Target.OzonAccount+"-other", 1),
	} {
		if w := h.post("/api/warehouse-mapping-reviews", body, h.cookie); w.Code != 404 {
			t.Fatal("unregistered tenant or account accepted")
		}
	}
	parseConfirmation(t, h.post(path, valid, h.cookie))
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
}

func TestWarehouseConfirmationRejectsUnsupportedAndHiddenPairs(t *testing.T) {
	h := newWarehouseConfirmationHarnessWithHistory(t, false)
	for _, pair := range []struct {
		moy, ozon string
		status    int
	}{
		{"warehouse-a", "ozon-rfbs", 400}, {"warehouse-a", "ozon-unknown", 400}, {"missing", "ozon-a", 404}, {"warehouse-a", "missing", 404},
	} {
		w := h.post("/api/warehouse-mapping-reviews", h.warehouseReviewBody(pair.moy, pair.ozon), h.cookie)
		if w.Code != pair.status {
			t.Fatalf("unsupported pair returned %d", w.Code)
		}
	}
	ctx := context.Background()
	ms := h.f.source
	now := time.Now().UTC()
	ms.StartedAt, ms.FinishedAt = now, now
	if _, err := h.f.store.SaveInventoryImport(ctx, ms, "warehouse-visible-after-registration-moy"); err != nil {
		t.Fatal(err)
	}
	oz := h.f.ozon
	oz.StartedAt, oz.FinishedAt = now, now
	if _, err := h.f.store.SaveOzonImport(ctx, oz, "warehouse-visible-after-registration-ozon"); err != nil {
		t.Fatal(err)
	}
	// The legacy mapping was confirmed before registration, so its details are outside the visible history.
	w := h.post("/api/warehouse-mapping-reviews", h.warehouseReviewBody("warehouse-a", "ozon-warehouse"), h.cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"WAREHOUSE_ALREADY_ASSIGNED"`) || strings.Contains(w.Body.String(), "warehouse-a") || strings.Contains(w.Body.String(), "ozon-warehouse") {
		t.Fatal("hidden mapping conflict was exposed or ignored")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
}

func TestWarehouseConfirmationRejectsStaleReview(t *testing.T) {
	for _, scenario := range []string{"moy-import", "ozon-import", "moy-warehouse-missing", "ozon-warehouse-missing", "moy-field", "ozon-field", "classification-rfbs", "classification-missing", "moy-fingerprint", "ozon-fingerprint", "mapping-conflict"} {
		t.Run(scenario, func(t *testing.T) {
			h := newWarehouseConfirmationHarness(t)
			review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
			ctx := context.Background()
			switch scenario {
			case "moy-import", "moy-warehouse-missing":
				ms := h.f.source
				ms.StartedAt = time.Now()
				ms.FinishedAt = ms.StartedAt
				if scenario == "moy-warehouse-missing" {
					filtered := make([]domain.ExternalEntity, 0, len(ms.Entities))
					for _, e := range ms.Entities {
						if e.Kind != "store" || e.ID != "warehouse-a" {
							filtered = append(filtered, e)
						}
					}
					ms.Entities = filtered
				}
				if _, err := h.f.store.SaveInventoryImport(ctx, ms, "warehouse-confirm-changed-moy"); err != nil {
					t.Fatal(err)
				}
			case "ozon-import", "ozon-warehouse-missing":
				oz := h.f.ozon
				oz.StartedAt = time.Now()
				oz.FinishedAt = oz.StartedAt
				if scenario == "ozon-warehouse-missing" {
					filtered := make([]domain.OzonWarehouse, 0, len(oz.Warehouses))
					for _, w := range oz.Warehouses {
						if w.ID != "ozon-a" {
							filtered = append(filtered, w)
						}
					}
					oz.Warehouses = filtered
					stocks := make([]domain.OzonStock, 0, len(oz.Stocks))
					for _, s := range oz.Stocks {
						if s.WarehouseID != "ozon-a" {
							stocks = append(stocks, s)
						}
					}
					oz.Stocks = stocks
				}
				if _, err := h.f.store.SaveOzonImport(ctx, oz, "warehouse-confirm-changed-ozon"); err != nil {
					t.Fatal(err)
				}
			case "moy-field":
				if _, err := h.f.store.Pool.Exec(ctx, `UPDATE source_entities SET name='changed synthetic name' WHERE import_id=$1 AND kind='store' AND external_id='warehouse-a'`, review.Review.MoyskladImportID); err != nil {
					t.Fatal(err)
				}
			case "ozon-field":
				if _, err := h.f.store.Pool.Exec(ctx, `UPDATE ozon_warehouse_observations SET name='changed synthetic name' WHERE import_id=$1 AND warehouse_id='ozon-a'`, review.Review.OzonImportID); err != nil {
					t.Fatal(err)
				}
			case "classification-rfbs":
				if _, err := h.f.store.Pool.Exec(ctx, `UPDATE ozon_warehouse_observations SET is_rfbs=true WHERE import_id=$1 AND warehouse_id='ozon-a'`, review.Review.OzonImportID); err != nil {
					t.Fatal(err)
				}
			case "classification-missing":
				if _, err := h.f.store.Pool.Exec(ctx, `UPDATE ozon_warehouse_observations SET is_rfbs=NULL WHERE import_id=$1 AND warehouse_id='ozon-a'`, review.Review.OzonImportID); err != nil {
					t.Fatal(err)
				}
			case "moy-fingerprint":
				if _, err := h.f.store.Pool.Exec(ctx, `UPDATE source_imports SET fingerprint='modified-fingerprint' WHERE id=$1`, review.Review.MoyskladImportID); err != nil {
					t.Fatal(err)
				}
			case "ozon-fingerprint":
				if _, err := h.f.store.Pool.Exec(ctx, `UPDATE source_imports SET fingerprint='modified-fingerprint' WHERE id=$1`, review.Review.OzonImportID); err != nil {
					t.Fatal(err)
				}
			case "mapping-conflict":
				scope := postgres.MatchScope{Tenant: h.f.req.Pool.Tenant, MoyskladAccount: h.f.req.Pool.MoyskladAccount, OzonAccount: h.f.req.Target.OzonAccount}
				if created, err := h.f.store.ConfirmWarehouseMapping(ctx, scope, "warehouse-a", "ozon-b", "synthetic legacy mapping"); err != nil || !created {
					t.Fatalf("legacy mapping setup: %v", err)
				}
			}
			w := h.post(warehouseConfirmationPath(review.Review.ID), confirmationBody("stale-key", "synthetic"), h.cookie)
			expected := "REVIEW_STALE"
			if scenario == "mapping-conflict" {
				expected = "MAPPING_CONFLICT"
			}
			if w.Code != 409 || !strings.Contains(w.Body.String(), `"`+expected+`"`) {
				t.Fatalf("stale review was not rejected: %d %s", w.Code, w.Body.String())
			}
			if scenario == "mapping-conflict" {
				h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 0, 0)
			} else {
				h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
			}
		})
	}
}

func TestWarehouseConfirmationRegistrationCannotBeChangedOrRemoved(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	ctx := context.Background()
	if _, err := h.f.store.Pool.Exec(ctx, `UPDATE stock_connections SET include_import_history=false WHERE tenant_id=$1 AND source='ozon'`, h.f.req.Pool.Tenant); err == nil {
		t.Fatal("immutable registration changed")
	}
	if _, err := h.f.store.Pool.Exec(ctx, `DELETE FROM stock_connections WHERE tenant_id=$1 AND source='ozon'`, h.f.req.Pool.Tenant); err == nil {
		t.Fatal("referenced registration deleted")
	}
	parseConfirmation(t, h.post(warehouseConfirmationPath(review.Review.ID), confirmationBody("registration-key", "synthetic"), h.cookie))
}

type warehouseConcurrentResult struct {
	status int
	body   string
}

func runWarehouseConcurrent(h *warehouseConfirmationHarness, paths, bodies []string) []warehouseConcurrentResult {
	start := make(chan struct{})
	results := make(chan warehouseConcurrentResult, len(paths))
	for i := range paths {
		go func(path, body string) {
			<-start
			w := h.post(path, body, h.cookie)
			results <- warehouseConcurrentResult{w.Code, w.Body.String()}
		}(paths[i], bodies[i])
	}
	close(start)
	out := make([]warehouseConcurrentResult, 0, len(paths))
	for range paths {
		out = append(out, <-results)
	}
	return out
}
func TestWarehouseConfirmationConcurrentIdenticalRequests(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	r := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	path := warehouseConfirmationPath(r.Review.ID)
	body := confirmationBody("concurrent-identical", "synthetic")
	paths, bodies := make([]string, 8), make([]string, 8)
	for i := range paths {
		paths[i], bodies[i] = path, body
	}
	responses := runWarehouseConcurrent(h, paths, bodies)
	var created, replayed int
	var mappingID int64
	for _, response := range responses {
		if response.status != 200 {
			t.Fatalf("concurrent request returned %d: %s", response.status, response.body)
		}
		var result domain.MappingConfirmationResult
		if err := json.Unmarshal([]byte(response.body), &result); err != nil {
			t.Fatal(err)
		}
		if mappingID == 0 {
			mappingID = result.MappingID
		}
		if result.MappingID != mappingID {
			t.Fatal("different mappings created")
		}
		if result.IdempotentReplay {
			replayed++
		} else {
			created++
		}
	}
	if created != 1 || replayed != 7 {
		t.Fatalf("expected one result and seven replays, got %d/%d", created, replayed)
	}
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
}
func TestWarehouseConfirmationConcurrentSameKeyDifferentBodies(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	r := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	path := warehouseConfirmationPath(r.Review.ID)
	responses := runWarehouseConcurrent(h, []string{path, path}, []string{confirmationBody("same-key", "first"), confirmationBody("same-key", "second")})
	var success, collision int
	for _, response := range responses {
		if response.status == 200 {
			success++
		} else if response.status == 409 && strings.Contains(response.body, `"IDEMPOTENCY_CONFLICT"`) {
			collision++
		} else {
			t.Fatalf("unexpected concurrent result %d %s", response.status, response.body)
		}
	}
	if success != 1 || collision != 1 {
		t.Fatal("different payloads both accepted")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
}
func TestWarehouseConfirmationConcurrentCompetingReviews(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	a := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	b := h.createWarehouseReview(t, "warehouse-a", "ozon-b")
	responses := runWarehouseConcurrent(h, []string{warehouseConfirmationPath(a.Review.ID), warehouseConfirmationPath(b.Review.ID)}, []string{confirmationBody("compete-a", "synthetic"), confirmationBody("compete-b", "synthetic")})
	var success, conflict int
	for _, response := range responses {
		if response.status == 200 {
			success++
		} else if response.status == 409 && (strings.Contains(response.body, `"MAPPING_CONFLICT"`) || strings.Contains(response.body, `"REVIEW_STALE"`)) {
			conflict++
		} else {
			t.Fatalf("unexpected competing result %d %s", response.status, response.body)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("competing mappings both succeeded")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
}
func TestWarehouseConfirmationReplayAfterReviewExpiration(t *testing.T) {
	t.Setenv("STYK_REVIEW_LIFETIME", "1s")
	h := newWarehouseConfirmationHarness(t)
	r := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	path := warehouseConfirmationPath(r.Review.ID)
	body := confirmationBody("replay-after-expiry", "synthetic")
	first := parseConfirmation(t, h.post(path, body, h.cookie))
	if delay := time.Until(r.Review.ExpiresAt) + 20*time.Millisecond; delay > 0 {
		time.Sleep(delay)
	}
	replay := parseConfirmation(t, h.post(path, body, h.cookie))
	if !replay.IdempotentReplay || replay.MappingID != first.MappingID {
		t.Fatal("expired review hid successful outcome")
	}
	changed := h.post(path, confirmationBody("replay-after-expiry", "changed"), h.cookie)
	if changed.Code != 409 || !strings.Contains(changed.Body.String(), `"IDEMPOTENCY_CONFLICT"`) {
		t.Fatal("expired review hid idempotency collision")
	}
	newKey := h.post(path, confirmationBody("new-after-expiry", "synthetic"), h.cookie)
	if newKey.Code != 409 || !strings.Contains(newKey.Body.String(), `"REVIEW_EXPIRED"`) {
		t.Fatal("expired review permitted new confirmation")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
}
func TestWarehouseConfirmationExpiredBeforeFirstAttempt(t *testing.T) {
	t.Setenv("STYK_REVIEW_LIFETIME", "30ms")
	h := newWarehouseConfirmationHarness(t)
	r := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	if delay := time.Until(r.Review.ExpiresAt) + 20*time.Millisecond; delay > 0 {
		time.Sleep(delay)
	}
	w := h.post(warehouseConfirmationPath(r.Review.ID), confirmationBody("expired-first", "synthetic"), h.cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"REVIEW_EXPIRED"`) {
		t.Fatal("expired review confirmed")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
}
func TestWarehouseConfirmationDoesNotReplaceExistingLegacyMapping(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	r := h.createWarehouseReview(t, "warehouse", "ozon-warehouse")
	w := h.post(warehouseConfirmationPath(r.Review.ID), confirmationBody("existing-pair", "synthetic"), h.cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"MAPPING_CONFLICT"`) {
		t.Fatal("legacy mapping replaced")
	}
	h.assertWarehouseRecords(t, "warehouse", 1, 1, 0, 0)
}

func TestWarehouseConfirmationRollsBackOnEachPersistenceFailure(t *testing.T) {
	cases := []struct{ name, table, constraint, check, key, explanation string }{
		{"mapping insert", "warehouse_mappings", "wh_confirm_test_mapping_failure", `moysklad_warehouse_id <> 'warehouse-a'`, "wh-mapping-failure", "synthetic"},
		{"history insert", "warehouse_mapping_history", "wh_confirm_test_history_failure", `moysklad_warehouse_id <> 'warehouse-a'`, "wh-history-failure", "synthetic"},
		{"audit insert", "warehouse_mapping_confirmation_audits", "wh_confirm_test_audit_failure", `explanation <> 'synthetic-audit-failure'`, "wh-audit-failure", "synthetic-audit-failure"},
		{"idempotency insert", "warehouse_mapping_confirmation_idempotency", "wh_confirm_test_outcome_failure", `idempotency_key <> 'wh-outcome-failure'`, "wh-outcome-failure", "synthetic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newWarehouseConfirmationHarness(t)
			r := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
			ctx := context.Background()
			add := fmt.Sprintf(`ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s) NOT VALID`, tc.table, tc.constraint, tc.check)
			drop := fmt.Sprintf(`ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s`, tc.table, tc.constraint)
			if _, err := h.f.store.Pool.Exec(ctx, add); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := h.f.store.Pool.Exec(context.Background(), drop); err != nil {
					t.Errorf("drop test constraint: %v", err)
				}
			})
			body := confirmationBody(tc.key, tc.explanation)
			w := h.post(warehouseConfirmationPath(r.Review.ID), body, h.cookie)
			if w.Code != 503 || !strings.Contains(w.Body.String(), `"PERSISTENCE_FAILURE"`) || strings.Contains(w.Body.String(), tc.constraint) {
				t.Fatalf("unexpected failure response: %d %s", w.Code, w.Body.String())
			}
			h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
			if _, err := h.f.store.Pool.Exec(ctx, drop); err != nil {
				t.Fatal(err)
			}
			parseConfirmation(t, h.post(warehouseConfirmationPath(r.Review.ID), body, h.cookie))
			h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
		})
	}
}

func TestWarehouseConfirmationAuditAndHistoryAreImmutable(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	r := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	result := parseConfirmation(t, h.post(warehouseConfirmationPath(r.Review.ID), confirmationBody("immutability", "synthetic"), h.cookie))
	statements := []string{
		`UPDATE warehouse_mapping_confirmation_audits SET explanation='changed' WHERE mapping_id=$1`,
		`DELETE FROM warehouse_mapping_confirmation_audits WHERE mapping_id=$1`,
		`UPDATE warehouse_mapping_confirmation_idempotency SET request_hash=repeat('0',64) WHERE mapping_id=$1`,
		`DELETE FROM warehouse_mapping_confirmation_idempotency WHERE mapping_id=$1`,
		`UPDATE warehouse_mapping_history SET provenance='changed' WHERE mapping_id=$1`,
		`DELETE FROM warehouse_mapping_history WHERE mapping_id=$1`,
		`UPDATE warehouse_mappings SET provenance='changed' WHERE id=$1`,
		`DELETE FROM warehouse_mappings WHERE id=$1`,
	}
	for _, statement := range statements {
		if _, err := h.f.store.Pool.Exec(context.Background(), statement, result.MappingID); err == nil {
			t.Fatalf("mutation unexpectedly succeeded: %s", statement)
		}
	}
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
}

func TestWarehouseReviewBoundHistoryRequiresAuditAndOutcome(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	r := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	ctx := context.Background()
	tx, err := h.f.store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	var id int64
	var at time.Time
	err = tx.QueryRow(ctx, `INSERT INTO warehouse_mappings(tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,version,provenance,confirmed_at) VALUES($1,$2,$3,'warehouse-a','ozon-a','active',1,'LOCAL_OPERATOR_SESSION',clock_timestamp()) RETURNING id,confirmed_at`, h.f.req.Pool.Tenant, h.f.req.Pool.MoyskladAccount, h.f.req.Target.OzonAccount).Scan(&id, &at)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO warehouse_mapping_history(mapping_id,version,tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,provenance,confirmed_at,moysklad_import_id,ozon_import_id,confirmation_review_id) VALUES($1,1,$2,$3,$4,'warehouse-a','ozon-a','active','LOCAL_OPERATOR_SESSION',$5,$6,$7,$8)`, id, h.f.req.Pool.Tenant, h.f.req.Pool.MoyskladAccount, h.f.req.Target.OzonAccount, at, r.Review.MoyskladImportID, r.Review.OzonImportID, r.Review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil {
		t.Fatal("review-bound mapping committed without audit and idempotency")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
}

func TestWarehouseConfirmationTimeoutLeavesNoPartialState(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	r := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	ctx := context.Background()
	blocker, err := h.f.store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, blocker)
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ozon:"+h.f.req.Target.OzonAccount); err != nil {
		t.Fatal(err)
	}
	h.f.store.Timeout = 50 * time.Millisecond
	body := confirmationBody("timeout-key", "synthetic")
	w := h.post(warehouseConfirmationPath(r.Review.ID), body, h.cookie)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"PERSISTENCE_FAILURE"`) {
		t.Fatalf("timeout response %d %s", w.Code, w.Body.String())
	}
	h.f.store.Timeout = 5 * time.Second
	if err = blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
	parseConfirmation(t, h.post(warehouseConfirmationPath(r.Review.ID), body, h.cookie))
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
}

func TestWarehouseConfirmationExpiryDuringTransactionRollsBack(t *testing.T) {
	t.Setenv("STYK_REVIEW_LIFETIME", "1s")
	h := newWarehouseConfirmationHarness(t)
	r := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	ctx := context.Background()
	_, err := h.f.store.Pool.Exec(ctx, `CREATE FUNCTION wh_confirm_test_delay_outcome() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.idempotency_key='wh-expiry-during-transaction' THEN PERFORM pg_sleep(1.25); END IF; RETURN NEW; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := h.f.store.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS wh_confirm_test_delay_outcome ON warehouse_mapping_confirmation_idempotency`); e != nil {
			t.Errorf("drop test trigger: %v", e)
		}
		if _, e := h.f.store.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS wh_confirm_test_delay_outcome()`); e != nil {
			t.Errorf("drop test function: %v", e)
		}
	})
	if _, err = h.f.store.Pool.Exec(ctx, `CREATE TRIGGER wh_confirm_test_delay_outcome BEFORE INSERT ON warehouse_mapping_confirmation_idempotency FOR EACH ROW EXECUTE FUNCTION wh_confirm_test_delay_outcome()`); err != nil {
		t.Fatal(err)
	}
	w := h.post(warehouseConfirmationPath(r.Review.ID), confirmationBody("wh-expiry-during-transaction", "synthetic"), h.cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"REVIEW_EXPIRED"`) {
		t.Fatalf("expiry during commit: %d %s", w.Code, w.Body.String())
	}
	h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
}

func TestWarehouseConfirmationCancellationDuringAuditRollsBack(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	ctx := context.Background()
	_, err := h.f.store.Pool.Exec(ctx, `CREATE FUNCTION wh_confirm_test_delay_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.explanation='synthetic-cancel' THEN PERFORM pg_sleep(5); END IF; RETURN NEW; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	drop := func() {
		if _, e := h.f.store.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS wh_confirm_test_delay_audit ON warehouse_mapping_confirmation_audits`); e != nil {
			t.Errorf("drop test trigger: %v", e)
		}
		if _, e := h.f.store.Pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS wh_confirm_test_delay_audit()`); e != nil {
			t.Errorf("drop test function: %v", e)
		}
	}
	t.Cleanup(drop)
	if _, err = h.f.store.Pool.Exec(ctx, `CREATE TRIGGER wh_confirm_test_delay_audit BEFORE INSERT ON warehouse_mapping_confirmation_audits FOR EACH ROW EXECUTE FUNCTION wh_confirm_test_delay_audit()`); err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	body := confirmationBody("cancel-during-audit", "synthetic-cancel")
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080"+warehouseConfirmationPath(review.Review.ID), strings.NewReader(body)).WithContext(requestCtx)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Origin", confirmationTestOrigin)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(h.cookie)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { w := httptest.NewRecorder(); h.h.ServeHTTP(w, req); done <- w }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	active := false
	for !active {
		select {
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("confirmation did not reach audit insertion")
		case <-ticker.C:
			if err = h.f.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND state='active' AND query LIKE 'INSERT INTO warehouse_mapping_confirmation_audits%')`).Scan(&active); err != nil {
				cancel()
				<-done
				t.Fatal(err)
			}
		}
	}
	cancel()
	w := <-done
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"PERSISTENCE_FAILURE"`) {
		t.Fatalf("cancellation response: %d %s", w.Code, w.Body.String())
	}
	h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
	drop()
	parseConfirmation(t, h.post(warehouseConfirmationPath(review.Review.ID), body, h.cookie))
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
}

func TestWarehouseConfirmationRecoversFromDisconnectedResponse(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	body := confirmationBody("warehouse-disconnected-response", "synthetic")
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080"+warehouseConfirmationPath(review.Review.ID), strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Origin", confirmationTestOrigin)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(h.cookie)
	writer := &disconnectedConfirmationWriter{header: make(http.Header)}
	h.h.ServeHTTP(writer, req)
	if !writer.attempted {
		t.Fatal("test did not interrupt the response")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
	replay := parseConfirmation(t, h.post(warehouseConfirmationPath(review.Review.ID), body, h.cookie))
	if !replay.IdempotentReplay || replay.MappingID <= 0 {
		t.Fatal("committed mapping was not recovered")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
}

func TestWarehouseConfirmationDoesNotProveStockSafety(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	ctx := context.Background()
	var poolsBefore, decisionsBefore, tasksBefore int
	if err := h.f.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM stock_pools WHERE tenant_id=$1),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1),(SELECT count(*) FROM outbox_tasks)`, h.f.req.Pool.Tenant).Scan(&poolsBefore, &decisionsBefore, &tasksBefore); err != nil {
		t.Fatal(err)
	}
	result := parseConfirmation(t, h.post(warehouseConfirmationPath(review.Review.ID), confirmationBody("warehouse-stock-safety", "synthetic"), h.cookie))
	var poolsAfter, decisionsAfter, tasksAfter int
	if err := h.f.store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM stock_pools WHERE tenant_id=$1),(SELECT count(*) FROM stock_decisions WHERE tenant_id=$1),(SELECT count(*) FROM outbox_tasks)`, h.f.req.Pool.Tenant).Scan(&poolsAfter, &decisionsAfter, &tasksAfter); err != nil || poolsAfter != poolsBefore || decisionsAfter != decisionsBefore || tasksAfter != tasksBefore {
		t.Fatal("warehouse confirmation created stock pool, decision or task")
	}
	h.f.req.Pool.WarehouseID = "warehouse-a"
	h.f.req.Target.WarehouseID = "ozon-a"
	decision := stockCalc(t, h.f)
	if decision.Status != domain.StockUnknown || decision.Quantity != nil || decision.Evidence.WarehouseMapping == nil || decision.Evidence.WarehouseMapping.ID != result.MappingID {
		t.Fatalf("warehouse attestation manufactured readiness: status %s", decision.Status)
	}
	for _, reason := range []string{commitment.ProofUnit, commitment.ProofBaseline, commitment.ProofAttribution, commitment.ProofUnassigned, commitment.ProofCompeting} {
		if !stockHasReason(decision, reason) {
			t.Fatalf("missing stock-safety reason %s", reason)
		}
	}
	_, err := h.f.store.Pool.Exec(ctx, `INSERT INTO stock_decisions(tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,policy_version,evaluated_at,expires_at,status,quantity,decision) SELECT tenant_id,pool_id,moysklad_account,moysklad_warehouse_id,item_kind,item_id,ozon_account,ozon_warehouse_id,ozon_product_id,policy_version,evaluated_at,evaluated_at+interval '1 minute','READY_CONDITIONAL',1,decision FROM stock_decisions WHERE id=$1`, decision.ID)
	if err == nil {
		t.Fatal("database accepted READY_CONDITIONAL after warehouse attestation")
	}
	if err = h.f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_tasks`).Scan(&tasksAfter); err != nil || tasksAfter != tasksBefore {
		t.Fatal("stock inspection scheduled a publication task")
	}
}

func TestWarehouseConfirmationSerializesWithLegacyCLI(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	start := make(chan struct{})
	apiResult := make(chan *httptest.ResponseRecorder, 1)
	type legacyResult struct {
		created bool
		err     error
	}
	legacy := make(chan legacyResult, 1)
	go func() {
		<-start
		apiResult <- h.post(warehouseConfirmationPath(review.Review.ID), confirmationBody("legacy-race", "synthetic"), h.cookie)
	}()
	go func() {
		<-start
		scope := postgres.MatchScope{Tenant: h.f.req.Pool.Tenant, MoyskladAccount: h.f.req.Pool.MoyskladAccount, OzonAccount: h.f.req.Target.OzonAccount}
		created, err := h.f.store.ConfirmWarehouseMapping(context.Background(), scope, "warehouse-a", "ozon-a", "synthetic legacy confirmation")
		legacy <- legacyResult{created, err}
	}()
	close(start)
	api, cli := <-apiResult, <-legacy
	if cli.err != nil {
		t.Fatalf("legacy CLI error: %v", cli.err)
	}
	if cli.created {
		if api.Code != 409 {
			t.Fatalf("review confirmed over legacy mapping: %d", api.Code)
		}
		h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 0, 0)
	} else {
		if api.Code != 200 {
			t.Fatalf("no successful mapping: %d", api.Code)
		}
		h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
	}
}

func warehouseSessionID(t *testing.T, h *warehouseConfirmationHarness, reviewID string) string {
	t.Helper()
	var session string
	if err := h.f.store.Pool.QueryRow(context.Background(), `SELECT session_audit_id FROM warehouse_mapping_reviews WHERE id=$1`, reviewID).Scan(&session); err != nil {
		t.Fatal(err)
	}
	return session
}
func insertManualWarehouseHistory(t *testing.T, tx pgx.Tx, tenant, moy, ozon string, review domain.WarehouseReview) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO warehouse_mappings(tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,version,provenance,confirmed_at) VALUES($1,$2,$3,'warehouse-a','ozon-a','active',1,'LOCAL_OPERATOR_SESSION',clock_timestamp()) RETURNING id`, tenant, moy, ozon).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO warehouse_mapping_history(mapping_id,version,tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,provenance,confirmed_at,moysklad_import_id,ozon_import_id,confirmation_review_id) SELECT id,version,tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,provenance,confirmed_at,$2,$3,$4 FROM warehouse_mappings WHERE id=$1`, id, review.MoyskladImportID, review.OzonImportID, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func insertManualWarehouseAudit(ctx context.Context, tx pgx.Tx, id int64, session string, shift time.Duration) error {
	_, err := tx.Exec(ctx, `INSERT INTO warehouse_mapping_confirmation_audits(mapping_id,mapping_version,review_id,tenant_id,ozon_account,moysklad_account,moysklad_warehouse_id,ozon_warehouse_id,ozon_import_id,moysklad_import_id,evidence_class,principal_type,session_audit_id,explanation,confirmed_at) SELECT h.mapping_id,h.version,h.confirmation_review_id,h.tenant_id,h.ozon_account,h.moysklad_account,h.moysklad_warehouse_id,h.ozon_warehouse_id,h.ozon_import_id,h.moysklad_import_id,'OPERATOR_ATTESTED','LOCAL_OPERATOR_SESSION',$2,'synthetic',h.confirmed_at+$3::interval FROM warehouse_mapping_history h WHERE h.mapping_id=$1`, id, session, shift.String())
	return err
}
func TestWarehouseReviewBoundCommitRejectsMismatchedEvidence(t *testing.T) {
	for _, scenario := range []string{"active-mapping-differs", "audit-timestamp-differs"} {
		t.Run(scenario, func(t *testing.T) {
			h := newWarehouseConfirmationHarness(t)
			review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
			session := warehouseSessionID(t, h, review.Review.ID)
			ctx := context.Background()
			tx, err := h.f.store.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rollbackLeaseTest(t, tx)
			id := insertManualWarehouseHistory(t, tx, h.f.req.Pool.Tenant, h.f.req.Pool.MoyskladAccount, h.f.req.Target.OzonAccount, review.Review)
			if scenario == "active-mapping-differs" {
				if _, err = tx.Exec(ctx, `UPDATE warehouse_mappings SET ozon_warehouse_id='ozon-b' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
			}
			shift := time.Duration(0)
			if scenario == "audit-timestamp-differs" {
				shift = time.Second
			}
			if err = insertManualWarehouseAudit(ctx, tx, id, session, shift); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO warehouse_mapping_confirmation_idempotency(review_id,session_audit_id,idempotency_key,request_hash,mapping_id,mapping_version) VALUES($1,$2,'integrity-test',repeat('a',64),$3,1)`, review.Review.ID, session, id)
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err == nil {
				t.Fatal("mismatched mapping or audit committed")
			}
			h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
		})
	}
}
func TestWarehouseConfirmationAuditRejectsCrossTenantAndAccount(t *testing.T) {
	for _, mode := range []string{"tenant", "account"} {
		t.Run(mode, func(t *testing.T) {
			source := newWarehouseConfirmationHarness(t)
			review := source.createWarehouseReview(t, "warehouse-a", "ozon-a")
			session := warehouseSessionID(t, source, review.Review.ID)
			owner := source
			tenant, moy, ozon := source.f.req.Pool.Tenant, source.f.req.Pool.MoyskladAccount, source.f.req.Target.OzonAccount
			if mode == "tenant" {
				owner = newWarehouseConfirmationHarness(t)
				tenant, moy, ozon = owner.f.req.Pool.Tenant, owner.f.req.Pool.MoyskladAccount, owner.f.req.Target.OzonAccount
			} else {
				ozon += "-other"
				if err := owner.f.store.RegisterStockConnection(context.Background(), tenant, "ozon", ozon, "synthetic registration", true); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			tx, err := owner.f.store.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rollbackLeaseTest(t, tx)
			id := insertManualWarehouseHistory(t, tx, tenant, moy, ozon, review.Review)
			err = insertManualWarehouseAudit(ctx, tx, id, session, 0)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
				t.Fatalf("cross-scope audit not rejected by FK: %v", err)
			}
			if err = tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var n int
			if err = owner.f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1 AND ozon_account=$2 AND moysklad_warehouse_id='warehouse-a'`, tenant, ozon).Scan(&n); err != nil || n != 0 {
				t.Fatal("cross-scope mapping partially persisted")
			}
		})
	}
}

func TestWarehouseConfirmationConcurrentDifferentKeysSameReview(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	path := warehouseConfirmationPath(review.Review.ID)
	responses := runWarehouseConcurrent(h, []string{path, path}, []string{confirmationBody("different-key-a", "synthetic"), confirmationBody("different-key-b", "synthetic")})
	var success, conflict int
	for _, response := range responses {
		if response.status == 200 {
			success++
		} else if response.status == 409 && (strings.Contains(response.body, `"REVIEW_STALE"`) || strings.Contains(response.body, `"MAPPING_CONFLICT"`)) {
			conflict++
		} else {
			t.Fatalf("unexpected result %d %s", response.status, response.body)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("same review confirmed twice")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 1, 1)
}

func TestWarehouseReviewBoundCommitRejectsExpiredReview(t *testing.T) {
	t.Setenv("STYK_REVIEW_LIFETIME", "1s")
	h := newWarehouseConfirmationHarness(t)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	session := warehouseSessionID(t, h, review.Review.ID)
	ctx := context.Background()
	tx, err := h.f.store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	id := insertManualWarehouseHistory(t, tx, h.f.req.Pool.Tenant, h.f.req.Pool.MoyskladAccount, h.f.req.Target.OzonAccount, review.Review)
	if err = insertManualWarehouseAudit(ctx, tx, id, session, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO warehouse_mapping_confirmation_idempotency(review_id,session_audit_id,idempotency_key,request_hash,mapping_id,mapping_version) VALUES($1,$2,'expiry-guard',repeat('a',64),$3,1)`, review.Review.ID, session, id); err != nil {
		t.Fatal(err)
	}
	if delay := time.Until(review.Review.ExpiresAt) + 30*time.Millisecond; delay > 0 {
		time.Sleep(delay)
	}
	err = tx.Commit(ctx)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" || pgerr.ConstraintName != "warehouse_review_unexpired" {
		t.Fatalf("database accepted expired review or returned unexpected error: %v", err)
	}
	h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
}

func TestWarehouseConfirmationConcurrentCompetingOzonWarehouse(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	a := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	b := h.createWarehouseReview(t, "warehouse-b", "ozon-a")
	responses := runWarehouseConcurrent(h, []string{warehouseConfirmationPath(a.Review.ID), warehouseConfirmationPath(b.Review.ID)}, []string{confirmationBody("target-compete-a", "synthetic"), confirmationBody("target-compete-b", "synthetic")})
	var success, conflict int
	for _, response := range responses {
		if response.status == 200 {
			success++
		} else if response.status == 409 && (strings.Contains(response.body, `"MAPPING_CONFLICT"`) || strings.Contains(response.body, `"REVIEW_STALE"`)) {
			conflict++
		} else {
			t.Fatalf("unexpected target conflict: %d %s", response.status, response.body)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("Ozon warehouse mapped twice")
	}
	var m, hist, auditCount, outcomeCount int
	err := h.f.store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM warehouse_mappings WHERE tenant_id=$1 AND ozon_warehouse_id='ozon-a'),(SELECT count(*) FROM warehouse_mapping_history WHERE tenant_id=$1 AND ozon_warehouse_id='ozon-a'),(SELECT count(*) FROM warehouse_mapping_confirmation_audits WHERE tenant_id=$1 AND ozon_warehouse_id='ozon-a'),(SELECT count(*) FROM warehouse_mapping_confirmation_idempotency x JOIN warehouse_mapping_confirmation_audits y ON y.mapping_id=x.mapping_id AND y.mapping_version=x.mapping_version WHERE y.tenant_id=$1 AND y.ozon_warehouse_id='ozon-a')`, h.f.req.Pool.Tenant).Scan(&m, &hist, &auditCount, &outcomeCount)
	if err != nil || m != 1 || hist != 1 || auditCount != 1 || outcomeCount != 1 {
		t.Fatal("Ozon target mapping state incorrect")
	}
}
func TestWarehouseConfirmationRequiresJSONContentType(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	path := warehouseConfirmationPath(review.Review.ID)
	for _, contentType := range []string{"", "text/plain", "application/jsonbad"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080"+path, strings.NewReader(confirmationBody("json-test", "synthetic")))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", confirmationTestOrigin)
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		r.AddCookie(h.cookie)
		w := httptest.NewRecorder()
		h.h.ServeHTTP(w, r)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"INVALID_REQUEST"`) {
			t.Fatal("unsupported content type accepted")
		}
	}
	h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
}
func TestWarehouseConfirmationCancelledWhenOperatorLogsOut(t *testing.T) {
	h := newWarehouseConfirmationHarness(t)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	ctx := context.Background()
	tx, err := h.f.store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ozon:"+h.f.req.Target.OzonAccount); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- h.post(warehouseConfirmationPath(review.Review.ID), confirmationBody("logout-key", "synthetic"), h.cookie)
	}()
	select {
	case <-done:
		t.Fatal("confirmation did not wait for import lock")
	case <-time.After(50 * time.Millisecond):
	}
	logout := h.call("DELETE", "/api/session", "", confirmationTestOrigin, "127.0.0.1:8080", "127.0.0.1:1234", h.cookie)
	if logout.Code != 204 {
		t.Fatalf("logout failed: %d", logout.Code)
	}
	select {
	case w := <-done:
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"PERSISTENCE_FAILURE"`) {
			t.Fatalf("in-flight request was not canceled: %d", w.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("logout did not cancel pending confirmation")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	h.assertWarehouseRecords(t, "warehouse-a", 0, 0, 0, 0)
	if w := h.post(warehouseConfirmationPath(review.Review.ID), confirmationBody("logout-key", "synthetic"), h.cookie); w.Code != 401 {
		t.Fatal("logged-out session remained authorized")
	}
}

func TestWarehouseConfirmationRejectsHiddenConflictAppearingAfterReview(t *testing.T) {
	h := newWarehouseConfirmationHarnessWithHistory(t, false)
	review := h.createWarehouseReview(t, "warehouse-a", "ozon-a")
	ctx := context.Background()
	scope := postgres.MatchScope{Tenant: h.f.req.Pool.Tenant, MoyskladAccount: h.f.req.Pool.MoyskladAccount, OzonAccount: h.f.req.Target.OzonAccount}
	created, err := h.f.store.ConfirmWarehouseMapping(ctx, scope, "warehouse-a", "ozon-b", "synthetic hidden legacy")
	if err != nil || !created {
		t.Fatalf("legacy fixture mapping: %v", err)
	}
	_, err = h.f.store.Pool.Exec(ctx, `UPDATE warehouse_mappings SET confirmed_at=(SELECT registered_at-interval '1 second' FROM stock_connections WHERE tenant_id=$1 AND source='ozon' AND account_id=$2) WHERE tenant_id=$1 AND moysklad_warehouse_id='warehouse-a'`, scope.Tenant, scope.OzonAccount)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.f.store.Pool.Exec(ctx, `UPDATE warehouse_mapping_history SET confirmed_at=(SELECT m.confirmed_at FROM warehouse_mappings m WHERE m.id=warehouse_mapping_history.mapping_id) WHERE tenant_id=$1 AND moysklad_warehouse_id='warehouse-a'`, scope.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	w := h.post(warehouseConfirmationPath(review.Review.ID), confirmationBody("hidden-conflict", "synthetic"), h.cookie)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"MAPPING_CONFLICT"`) || strings.Contains(w.Body.String(), "ozon-b") || strings.Contains(w.Body.String(), "synthetic hidden legacy") {
		t.Fatal("hidden conflict was ignored or disclosed")
	}
	h.assertWarehouseRecords(t, "warehouse-a", 1, 1, 0, 0)
}
