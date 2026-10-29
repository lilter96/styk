// Command seed creates only synthetic observations in a specifically named local E2E database.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

func run() error {
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/styk_mapping_ui_e2e_v1" {
		return fmt.Errorf("expected dedicated local styk_mapping_ui_e2e_v1 database")
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, u.String(), 5*time.Second)
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	for _, c := range []struct{ source, account string }{{"moysklad", "ui-e2e-moy"}, {"ozon", "ui-e2e-ozon"}} {
		if err := db.RegisterStockConnection(ctx, "ui-e2e-tenant", c.source, c.account, "synthetic browser test", true); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	start := now.Add(-time.Minute)
	zero := "0"
	_, err = db.SaveInventoryImport(ctx, domain.InventoryImport{Account: "ui-e2e-moy", StartedAt: start, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "parent", Article: "other"}, {Kind: "variant", ID: "variant", ParentID: "parent", Article: "different", Code: "synthetic-code"}, {Kind: "store", ID: "moy-warehouse", Name: "Synthetic source warehouse"}}, WarehouseStock: []domain.WarehouseStockObservation{{Kind: "variant", ExternalID: "variant", WarehouseID: "moy-warehouse", Stock: &zero, Reserve: &zero, InTransit: &zero}}}, "ui-e2e-moy-warehouse-v1")
	if err != nil {
		return err
	}
	fbs, rfbs := false, true
	_, err = db.SaveOzonImport(ctx, domain.OzonImport{Account: "ui-e2e-ozon", StartedAt: start, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "ozon-product", OfferID: "offer-synthetic"}}, Warehouses: []domain.OzonWarehouse{{ID: "ozon-warehouse", Name: "Synthetic FBS", IsRFBS: &fbs}, {ID: "ozon-rfbs", Name: "Synthetic rFBS", IsRFBS: &rfbs}}, Stocks: []domain.OzonStock{{ProductID: "ozon-product", OfferID: "offer-synthetic", WarehouseID: "ozon-warehouse", Present: 0, Reserved: 0, FreeStock: 0}}}, "ui-e2e-ozon-warehouse-v1")
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "synthetic seed failed:", err)
		os.Exit(1)
	}
}
