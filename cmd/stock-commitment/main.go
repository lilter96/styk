package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: stock-commitment register|calculate|inspect [flags]")
	}
	action := os.Args[1]
	if action != "register" && action != "calculate" && action != "inspect" {
		return errors.New("action must be register, calculate or inspect")
	}
	f := flag.NewFlagSet(action, flag.ContinueOnError)
	tenant := f.String("tenant", "", "trusted administrative tenant scope")
	source := f.String("source", "", "registration source: moysklad or ozon")
	account := f.String("account", "", "registration account ID")
	provenance := f.String("provenance", "", "administrator and ownership verification reason")
	includeHistory := f.Bool("include-import-history", false, "attest that existing source/account imports belong to this tenant")
	moy := f.String("moysklad-account", "", "registered MoySklad account ID")
	ozon := f.String("ozon-account", "", "registered Ozon account ID")
	warehouse := f.String("moysklad-warehouse", "", "MoySklad warehouse ID")
	ozonWarehouse := f.String("ozon-warehouse", "", "Ozon FBS warehouse ID")
	kind := f.String("item-kind", "", "product or variant")
	item := f.String("item-id", "", "MoySklad item ID")
	product := f.String("ozon-product", "", "Ozon product ID")
	policyVersion := f.Int("policy-version", 1, "calculation policy version")
	unit := f.String("unit", "piece", "source and sale unit")
	safety := f.String("safety-stock", "0", "nonnegative whole pieces")
	maxAge := f.Duration("max-observation-age", time.Hour, "maximum local observation age")
	lifetime := f.Duration("decision-lifetime", 5*time.Minute, "maximum conditional decision lifetime")
	id := f.Int64("decision", 0, "decision ID for inspection")
	if err := f.Parse(os.Args[2:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := postgres.Open(ctx, cfg.DatabaseURL, cfg.QueryTimeout)
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	switch action {
	case "register":
		if err := db.RegisterStockConnection(ctx, *tenant, *source, *account, *provenance, *includeHistory); err != nil {
			return err
		}
		return enc.Encode(struct{ Registered bool }{true})
	case "inspect":
		d, err := db.StockDecision(ctx, *tenant, *ozon, *id)
		if err != nil {
			return err
		}
		return enc.Encode(d)
	default:
		r := domain.StockCalculationRequest{
			Pool:   domain.StockPool{Tenant: *tenant, MoyskladAccount: *moy, WarehouseID: *warehouse, ItemKind: *kind, ItemID: *item},
			Target: domain.StockTarget{OzonAccount: *ozon, WarehouseID: *ozonWarehouse, ProductID: *product},
			Policy: domain.StockCalculationPolicy{Version: *policyVersion, Unit: *unit, SafetyStock: *safety, MaxObservationAge: *maxAge, DecisionLifetime: *lifetime},
		}
		d, err := (application.StockCalculator{Store: db}).Run(ctx, r, time.Now().UTC())
		if err != nil {
			return err
		}
		return enc.Encode(d)
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "stock-commitment:", err)
		os.Exit(1)
	}
}
