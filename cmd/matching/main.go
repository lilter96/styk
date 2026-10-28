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

	"example.com/styk/internal/config"
	"example.com/styk/internal/matching"
	"example.com/styk/internal/postgres"
)

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: matching inspect|confirm [flags]")
	}
	action := os.Args[1]
	if action != "inspect" && action != "confirm" {
		return errors.New("action must be inspect or confirm")
	}
	f := flag.NewFlagSet(action, flag.ContinueOnError)
	tenant := f.String("tenant", "", "tenant identifier")
	ozon := f.String("ozon-account", "", "Ozon client ID")
	moy := f.String("moysklad-account", "", "MoySklad account ID")
	product := f.String("product", "", "Ozon product ID")
	kind := f.String("target-kind", "", "product or variant")
	target := f.String("target-id", "", "MoySklad item ID")
	actor := f.String("provenance", "", "operator and reason")
	if err := f.Parse(os.Args[2:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	scope := postgres.MatchScope{Tenant: *tenant, OzonAccount: *ozon, MoyskladAccount: *moy}
	if err := scope.Validate(); err != nil {
		return err
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
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if action == "confirm" {
		result, err := db.ConfirmMatch(ctx, scope, *product, *kind, *target, *actor)
		if err != nil {
			return err
		}
		return encoder.Encode(struct {
			Confirmation string
			Result       any
		}{"confirmed", result})
	}
	data, err := db.MatchData(ctx, scope)
	if err != nil {
		return err
	}
	engine := matching.New(data.Targets, data.Mappings)
	results := make([]any, 0)
	for _, source := range data.Sources {
		if *product != "" && source.Identity.ID != *product {
			continue
		}
		results = append(results, engine.Match(*tenant, source, *moy))
	}
	return encoder.Encode(struct {
		OzonImportID, MoyskladImportID int64
		Results                        []any
	}{data.OzonImportID, data.MoyskladImportID, results})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "matching:", err)
		os.Exit(1)
	}
}
