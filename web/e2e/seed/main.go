// Command seed creates only synthetic observations in a specifically named local E2E database.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"example.com/styk/internal/domain"
	"example.com/styk/internal/postgres"
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
	_, err = db.SaveInventoryImport(ctx, domain.InventoryImport{Account: "ui-e2e-moy", StartedAt: start, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "parent", Article: "other"}, {Kind: "variant", ID: "variant", ParentID: "parent", Article: "different", Code: "synthetic-code"}}}, "ui-e2e-moy-fingerprint")
	if err != nil {
		return err
	}
	_, err = db.SaveOzonImport(ctx, domain.OzonImport{Account: "ui-e2e-ozon", StartedAt: start, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "ozon-product", OfferID: "offer-synthetic"}}}, "ui-e2e-ozon-fingerprint")
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "synthetic seed failed:", err)
		os.Exit(1)
	}
}
