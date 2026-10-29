package main

import (
	"context"
	"fmt"
	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"log/slog"
	"os"
)

func main() {
	if err := run(); err != nil {
		slog.Error("demo failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, c.DatabaseURL, c.QueryTimeout)
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	id, err := db.Submit(ctx, domain.Observation{OperationKey: "demo-inventory-1", Source: "demo", SKU: "demo-sku", Quantity: 42}, c.MaxAttempts)
	if err != nil {
		return err
	}
	fmt.Printf("observation %d submitted (operation key demo-inventory-1)\n", id)
	return nil
}
