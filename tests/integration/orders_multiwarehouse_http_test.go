//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

func TestOrdersMultipleWarehousesUseNormalProviderPipeline(t *testing.T) {
	f := multiWarehouseHTTPFixture(t)
	preview := multiWarehousePreview(t, f)
	if len(preview.Decisions) != 3 || !preview.Decisions[0].Ready || !preview.Decisions[1].Ready || preview.Decisions[2].Ready {
		t.Fatalf("warehouse isolation lost: %+v", preview.Decisions)
	}
	if preview.Decisions[1].Commands[0].CustomerOrder.StoreID != ordersProcessID(44) {
		t.Fatal("second warehouse used global config store")
	}
	f.approve(t, preview)
	for range 4 {
		time.Sleep(3 * time.Millisecond)
		if ran, err := f.runner.RunNext(f.ctx); err != nil || !ran {
			history, historyErr := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 10})
			if historyErr != nil {
				t.Fatal(historyErr)
			}
			for _, operation := range history.Operations {
				for _, target := range operation.Targets {
					t.Logf("posting=%s state=%s code=%s dispatch=%d", target.Command.PostingNumber, target.State, target.Code, target.DispatchCount)
				}
			}
			t.Fatalf("normal worker: %v %v", ran, err)
		}
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	stores := make(map[string]bool, 2)
	for _, native := range f.provider.base.native {
		store := native.body["store"].(map[string]any)["meta"].(map[string]any)["href"].(string)
		stores[store] = true
	}
	if len(stores) != 2 {
		t.Fatalf("expected two native orders on distinct warehouses: %v", stores)
	}
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_work WHERE state='RECONCILED' AND dispatch_count=1`, 2)
}

func TestOrdersWarehouseDriftBlocksQueuedProviderWrite(t *testing.T) {
	f := multiWarehouseHTTPFixture(t)
	preview := multiWarehousePreview(t, f)
	preview.Decisions = preview.Decisions[1:2]
	preview.ID += "-second-only"
	var err error
	preview, err = f.db.SaveOrderPreview(f.ctx, preview)
	if err != nil {
		t.Fatal(err)
	}
	f.approve(t, preview)
	scope := f.config.Scope
	// Adversarial drift of an unreviewed CLI fixture: the old history no
	// longer confirms its target. Production review-confirmed links are immutable.
	if _, err = f.db.Pool.Exec(f.ctx, `UPDATE warehouse_mappings SET moysklad_warehouse_id=$4 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND ozon_warehouse_id='3002'`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, ordersProcessID(45)); err != nil {
		t.Fatal(err)
	}
	f.run(t, orders.StateBlocked)
	assertOrdersRowCount(t, f.ordersStoreFixture, `SELECT count(*) FROM orders_write_intents`, 0)
}

func multiWarehouseHTTPFixture(t *testing.T) positionHTTPFixture {
	t.Helper()
	f := newPositionHTTPFixture(t)
	scope := f.config.Scope
	now, rfbs := time.Now().UTC(), false
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: ordersProcessID(8), Name: "Synthetic item", Article: "process-offer"}, {Kind: "store", ID: ordersProcessID(4), Name: "Synthetic store one"}, {Kind: "store", ID: ordersProcessID(44), Name: "Synthetic store two"}, {Kind: "store", ID: ordersProcessID(45), Name: "Synthetic store three"}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "1001", OfferID: "process-offer", SKU: "2001"}}, Warehouses: []domain.OzonWarehouse{{ID: "3001", Name: "Synthetic warehouse one", IsRFBS: &rfbs}, {ID: "3002", Name: "Synthetic warehouse two", IsRFBS: &rfbs}, {ID: "3999", Name: "Synthetic unmapped warehouse", IsRFBS: &rfbs}}}
	if _, err := f.db.SaveInventoryImport(f.ctx, source, "multi-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SaveOzonImport(f.ctx, market, "multi-market"); err != nil {
		t.Fatal(err)
	}
	match := postgres.MatchScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}
	if _, err := f.db.ConfirmWarehouseMapping(f.ctx, match, ordersProcessID(44), "3002", "multi-fixture"); err != nil {
		t.Fatal(err)
	}
	config := f.config
	config.StoreID = ""
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "without-global-store"}, now)
	if err != nil {
		t.Fatal(err)
	}
	f.provider.base.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/entity/store" {
			rows := make([]any, 0, 3)
			for _, id := range []string{ordersProcessID(4), ordersProcessID(44), ordersProcessID(45)} {
				row := f.provider.base.reference("entity/store", "store", id)
				row["id"], row["accountId"], row["name"] = id, scope.MoyskladAccount, "Synthetic multi warehouse"
				rows = append(rows, row)
			}
			f.provider.base.page(w, rows, len(rows), 1000, 0)
			return
		}
		if r.URL.Path != "/v3/posting/fbs/get" {
			f.provider.serve(w, r)
			return
		}
		var body struct {
			Posting string `json:"posting_number"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		row := f.provider.base.posting(body.Posting, "FBS")
		warehouse := "3001"
		if strings.HasSuffix(body.Posting, "-2") {
			warehouse = "3002"
		}
		if strings.HasSuffix(body.Posting, "-3") {
			warehouse = "3999"
		}
		row["delivery_method"].(map[string]any)["warehouse_id"] = warehouse
		f.provider.base.reply(w, map[string]any{"result": row})
	})
	return f
}

func multiWarehousePreview(t *testing.T, f positionHTTPFixture) orders.Preview {
	t.Helper()
	binding, err := f.factory.ResolveOrder(f.ctx, f.config.Scope, "ozon", f.config.Scope.OzonAccount, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, number := range []string{"multi-1", "multi-2", "multi-3"} {
		posting, e := binding.Client.ReadOrderPosting(f.ctx, f.config.Scope, number)
		if e != nil {
			t.Fatal(e)
		}
		ordersStoreCommit(t, f.ordersStoreFixture, posting, "source-"+number)
	}
	preview, err := f.service.Preview(f.ctx, orders.PreviewRequest{Scope: f.config.Scope, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, PostingNumbers: []string{"multi-1", "multi-2", "multi-3"}})
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func TestOrdersWarehouseSettingsPageIsScopedAndPaginated(t *testing.T) {
	f := multiWarehouseHTTPFixture(t)
	page, err := f.db.OrderWarehouses(f.ctx, f.config.Scope, orders.PageRequest{Limit: 1})
	if err != nil || page.Total != 3 || page.Confirmed != 2 || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("first warehouse page: %+v %v", page, err)
	}
	if page.Items[0].Status != "confirmed" || page.Items[0].StoreName == "" {
		t.Fatal("canonical mapping lost its name or confirmation")
	}
	second, err := f.db.OrderWarehouses(f.ctx, f.config.Scope, orders.PageRequest{Limit: 2, Cursor: page.NextCursor})
	if err != nil || len(second.Items) != 2 || second.Items[1].Status != "missing" {
		t.Fatalf("remaining warehouses: %+v %v", second, err)
	}
	scope := f.config.Scope
	scope.OzonAccount = "foreign"
	if _, err = f.db.OrderWarehouses(f.ctx, scope, orders.PageRequest{Limit: 10}); err == nil {
		t.Fatal("foreign account exposed mappings")
	}
}
