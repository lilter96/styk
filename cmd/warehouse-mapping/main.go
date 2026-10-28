package main

import (
	"context"
	"example.com/styk/internal/config"
	"example.com/styk/internal/postgres"
	"flag"
	"fmt"
	"os"
)

func run() error {
	tenant := flag.String("tenant", "", "tenant ID")
	moy := flag.String("moysklad-account", "", "MoySklad account")
	ozon := flag.String("ozon-account", "", "Ozon account")
	source := flag.String("moysklad-warehouse", "", "source warehouse ID")
	dest := flag.String("ozon-warehouse", "", "destination warehouse ID")
	provenance := flag.String("provenance", "", "operator and reason")
	flag.Parse()
	c, err := config.Load()
	if err != nil {
		return err
	}
	db, err := postgres.Open(context.Background(), c.DatabaseURL, c.QueryTimeout)
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	created, err := db.ConfirmWarehouseMapping(context.Background(), postgres.MatchScope{Tenant: *tenant, MoyskladAccount: *moy, OzonAccount: *ozon}, *source, *dest, *provenance)
	if err != nil {
		return err
	}
	if created {
		fmt.Println("warehouse mapping confirmed")
	} else {
		fmt.Println("warehouse mapping already confirmed")
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "warehouse mapping:", err)
		os.Exit(1)
	}
}
