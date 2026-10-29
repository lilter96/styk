//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/testpublication"
)

type shadowUncertaintyResponse struct {
	State   string `json:"state"`
	Targets []struct {
		ProductID   string `json:"product_id"`
		WarehouseID string `json:"warehouse_id"`
		Uncertain   bool   `json:"uncertain_write"`
		Historical  bool   `json:"historical_uncertain_write"`
	} `json:"targets"`
}

func uncertaintyShadowClient(t *testing.T, db *postgres.Store) func(domain.StockSyncScope, int) shadowUncertaintyResponse {
	t.Helper()
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", "")
	t.Setenv("OZON_CLIENT_ID", "")
	t.Setenv("OZON_API_KEY", "")
	t.Setenv("MOYSKLAD_ACCOUNT_ID", "")
	t.Setenv("MOYSKLAD_TOKEN", "")
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	origin := "http://" + server.Listener.Addr().String()
	secret := strings.Repeat("s", 32)
	h, err := console.Handler(db, secret, origin)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = h
	server.Start()
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar
	req, err := http.NewRequest(http.MethodPost, origin+"/api/session", strings.NewReader(fmt.Sprintf(`{"secret":%q}`, secret)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err = resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("session status: %d", resp.StatusCode)
	}
	return func(scope domain.StockSyncScope, expected int) shadowUncertaintyResponse {
		t.Helper()
		q := url.Values{"tenant": {scope.Tenant}, "moysklad_account": {scope.MoyskladAccount}, "ozon_account": {scope.OzonAccount}}
		response, e := client.Get(origin + "/api/stock-sync/shadow?" + q.Encode())
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := response.Body.Close(); e != nil {
				t.Errorf("close shadow response: %v", e)
			}
		}()
		if response.StatusCode != expected {
			t.Fatalf("shadow status=%d want=%d", response.StatusCode, expected)
		}
		var result shadowUncertaintyResponse
		if expected == http.StatusOK {
			if e = json.NewDecoder(response.Body).Decode(&result); e != nil {
				t.Fatal(e)
			}
		}
		return result
	}
}

func requireShadowUncertainty(t *testing.T, r shadowUncertaintyResponse, product, warehouse string, unresolved, historical bool) {
	t.Helper()
	for _, target := range r.Targets {
		if target.ProductID == product && target.WarehouseID == warehouse {
			if target.Uncertain != unresolved || target.Historical != historical {
				t.Fatalf("wrong target uncertainty: %+v", target)
			}
			return
		}
	}
	t.Fatalf("target %s/%s missing from shadow", product, warehouse)
}

func syntheticIndependentShadowScope(t *testing.T, db *postgres.Store) domain.StockSyncScope {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UnixNano())
	scope := domain.StockSyncScope{Tenant: "uncertain-tenant-" + suffix, MoyskladAccount: "uncertain-moy-" + suffix, OzonAccount: "uncertain-ozon-" + suffix}
	now := time.Now().UTC().Truncate(time.Microsecond)
	stock, zero := "10", "0"
	rfbs, archived := false, false
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "other-item", Article: "other-offer"}, {Kind: "store", ID: "other-store"}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: "other-item", WarehouseID: "other-store", Stock: &stock, Reserve: &zero, InTransit: &zero}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: testpublication.OzonProduct, OfferID: "other-offer", SKU: "999", Archived: &archived}}, Warehouses: []domain.OzonWarehouse{{ID: testpublication.OzonWarehouse, IsRFBS: &rfbs}}, Stocks: []domain.OzonStock{{ProductID: testpublication.OzonProduct, OfferID: "other-offer", SKU: "999", WarehouseID: testpublication.OzonWarehouse, Present: 1, Reserved: 0, FreeStock: 1}}}
	if _, err := db.SaveInventoryImport(ctx, source, "other-source-"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "other-market-"+suffix); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err := db.RegisterStockConnection(ctx, scope.Tenant, c.provider, c.account, "synthetic", true); err != nil {
			t.Fatal(err)
		}
	}
	match := postgres.MatchScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}
	if _, err := db.ConfirmMatch(ctx, match, testpublication.OzonProduct, "product", "other-item", "synthetic product mapping"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ConfirmWarehouseMapping(ctx, match, "other-store", testpublication.OzonWarehouse, "synthetic warehouse mapping"); err != nil {
		t.Fatal(err)
	}
	return scope
}

func addUnrelatedShadowProduct(t *testing.T, db *postgres.Store, scope domain.StockSyncScope) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	stock, zero := "7", "0"
	rfbs, archived := false, false
	const product = "27182818"
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: testpublication.SourceProduct, Article: "synthetic"}, {Kind: "product", ID: "other-product", Article: "other-offer"}, {Kind: "store", ID: testpublication.SourceWarehouse}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: testpublication.SourceProduct, WarehouseID: testpublication.SourceWarehouse, Stock: &stock, Reserve: &zero, InTransit: &zero}, {Kind: "product", ExternalID: "other-product", WarehouseID: testpublication.SourceWarehouse, Stock: &stock, Reserve: &zero, InTransit: &zero}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, Archived: &archived}, {ProductID: product, OfferID: "other-offer", SKU: "271829", Archived: &archived}}, Warehouses: []domain.OzonWarehouse{{ID: testpublication.OzonWarehouse, Status: "created", IsRFBS: &rfbs}}, Stocks: []domain.OzonStock{{ProductID: testpublication.OzonProduct, OfferID: testpublication.OzonOffer, SKU: testpublication.OzonSKU, WarehouseID: testpublication.OzonWarehouse, Present: 0, Reserved: 0, FreeStock: 0}, {ProductID: product, OfferID: "other-offer", SKU: "271829", WarehouseID: testpublication.OzonWarehouse, Present: 0, Reserved: 0, FreeStock: 0}}}
	if _, err := db.SaveInventoryImport(ctx, source, "shadow-other-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "shadow-other-market"); err != nil {
		t.Fatal(err)
	}
	match := postgres.MatchScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}
	if _, err := db.ConfirmMatch(ctx, match, product, "product", "other-product", "synthetic unrelated product mapping"); err != nil {
		t.Fatal(err)
	}
	return product
}

func TestStockShadowDistinguishesRecoveredAndUnresolvedUnknown(t *testing.T) {
	db, review := controlledFixture(t)
	ctx := context.Background()
	scope := review.StockSyncScope
	controlledEnable(t, db, review)
	now := time.Now().UTC().Truncate(time.Microsecond)
	claim, err := db.NextControlledStockPublication(ctx, now)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	target := claim.Targets[0]
	if err = db.FinishStockTarget(ctx, scope, claim.ID, target.ID, "UNKNOWN", "SYNTHETIC_UNKNOWN", now); err != nil {
		t.Fatal(err)
	}
	get := uncertaintyShadowClient(t, db)
	requireShadowUncertainty(t, get(scope, http.StatusOK), target.ProductID, target.WarehouseID, true, false)
	wrong := scope
	wrong.Tenant = "other-tenant"
	get(wrong, http.StatusNotFound)
	other := syntheticIndependentShadowScope(t, db)
	requireShadowUncertainty(t, get(other, http.StatusOK), target.ProductID, target.WarehouseID, false, false)

	// Simulate a corrupt, missing fence in the isolated schema. Missing audit
	// must remain unresolved even if the protective row has disappeared.
	if _, err = db.Pool.Exec(ctx, `ALTER TABLE stock_publication_target_fences DISABLE TRIGGER stock_publication_fence_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `DELETE FROM stock_publication_target_fences WHERE target_id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `ALTER TABLE stock_publication_target_fences ENABLE TRIGGER stock_publication_fence_guard`); err != nil {
		t.Fatal(err)
	}
	requireShadowUncertainty(t, get(scope, http.StatusOK), target.ProductID, target.WarehouseID, true, false)
	if _, err = db.Pool.Exec(ctx, `INSERT INTO stock_publication_target_fences(ozon_account,product_id,warehouse_id,target_id) VALUES($1,$2,$3,$4)`, scope.OzonAccount, target.ProductID, target.WarehouseID, target.ID); err != nil {
		t.Fatal(err)
	}

	zero := int64(0)
	if err = db.RecordUnknownStockReadback(ctx, scope, claim.ID, target.ID, &zero, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	request := domain.StockRecoveryRequest{StockSyncScope: scope, OperationID: claim.ID, SessionID: "synthetic-session", IdempotencyKey: "synthetic-shadow-recovery", ExpectedReadbackAt: now.Add(time.Second), ExpectedQuantity: "0", Reason: "Synthetic acceptance of previous uncertainty", Acknowledge: true, WorkerStopped: true}
	if _, err = db.SupersedeTestUnknown(ctx, request, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	after := get(scope, http.StatusOK)
	requireShadowUncertainty(t, after, target.ProductID, target.WarehouseID, false, true)
	if after.State != "TEST_SUPERSEDED_REVIEW_REQUIRED" {
		t.Fatalf("unexpected recovered state %q", after.State)
	}
	original, err := db.StockPublication(ctx, scope, claim.ID)
	if err != nil || original.State != "UNKNOWN" {
		t.Fatalf("historical outcome was changed: %+v %v", original, err)
	}

	// A stale fence for the recovered target is inconsistent and must fail closed.
	if _, err = db.Pool.Exec(ctx, `INSERT INTO stock_publication_target_fences(ozon_account,product_id,warehouse_id,target_id) VALUES($1,$2,$3,$4)`, scope.OzonAccount, target.ProductID, target.WarehouseID, target.ID); err != nil {
		t.Fatal(err)
	}
	requireShadowUncertainty(t, get(scope, http.StatusOK), target.ProductID, target.WarehouseID, true, false)
	if _, err = db.Pool.Exec(ctx, `DELETE FROM stock_publication_target_fences WHERE target_id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}

	// A new queued write has its own legitimate fence. It does not make the
	// already recovered UNKNOWN unresolved again.
	queuedID := strings.Repeat("b", 32)
	if _, err = db.Pool.Exec(ctx, `INSERT INTO stock_publications(id,tenant_id,moysklad_account,ozon_account,review_id,created_at) VALUES($1,$2,$3,$4,$5,$6)`, queuedID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, review.ID, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO stock_publication_targets(operation_id,ozon_account,product_id,warehouse_id,quantity,state,decision) VALUES($1,$2,$3,$4,'7','QUEUED','{}')`, queuedID, scope.OzonAccount, target.ProductID, target.WarehouseID); err != nil {
		t.Fatal(err)
	}
	requireShadowUncertainty(t, get(scope, http.StatusOK), target.ProductID, target.WarehouseID, false, true)
	if _, err = db.Pool.Exec(ctx, `UPDATE stock_publication_targets SET state='CANCELLED' WHERE operation_id=$1`, queuedID); err != nil {
		t.Fatal(err)
	}

	// A later reconciled result must not erase the old UNKNOWN audit or revive its fence.
	newID := strings.Repeat("a", 32)
	if _, err = db.Pool.Exec(ctx, `INSERT INTO stock_publications(id,tenant_id,moysklad_account,ozon_account,review_id,created_at) VALUES($1,$2,$3,$4,$5,$6)`, newID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, review.ID, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO stock_publication_targets(operation_id,ozon_account,product_id,warehouse_id,quantity,state,decision) VALUES($1,$2,$3,$4,'7','RECONCILED','{}')`, newID, scope.OzonAccount, target.ProductID, target.WarehouseID); err != nil {
		t.Fatal(err)
	}
	reconciled := get(scope, http.StatusOK)
	requireShadowUncertainty(t, reconciled, target.ProductID, target.WarehouseID, false, true)
	if reconciled.State != "RECONCILED" {
		t.Fatalf("latest reconciliation was not reflected: %q", reconciled.State)
	}
	var reconciledID int64
	if err = db.Pool.QueryRow(ctx, `SELECT id FROM stock_publication_targets WHERE operation_id=$1`, newID).Scan(&reconciledID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO stock_publication_target_fences(ozon_account,product_id,warehouse_id,target_id) VALUES($1,$2,$3,$4)`, scope.OzonAccount, target.ProductID, target.WarehouseID, reconciledID); err != nil {
		t.Fatal(err)
	}
	requireShadowUncertainty(t, get(scope, http.StatusOK), target.ProductID, target.WarehouseID, true, true)
	if _, err = db.Pool.Exec(ctx, `DELETE FROM stock_publication_target_fences WHERE target_id=$1`, reconciledID); err != nil {
		t.Fatal(err)
	}

	otherProduct := addUnrelatedShadowProduct(t, db, scope)
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "")
	both := get(scope, http.StatusOK)
	requireShadowUncertainty(t, both, target.ProductID, target.WarehouseID, false, true)
	requireShadowUncertainty(t, both, otherProduct, target.WarehouseID, false, false)
}
