package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/postgres"
)

func duration(name string, fallback time.Duration) (time.Duration, error) {
	if v := os.Getenv(name); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("%s must be a positive duration", name)
		}
		return d, nil
	}
	return fallback, nil
}

func run() error {
	if len(os.Args) < 2 || (os.Args[1] != "import" && os.Args[1] != "correlate") {
		return errors.New("usage: moysklad-orders import|correlate [flags]")
	}
	action := os.Args[1]
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	tenant := flags.String("tenant", "", "trusted tenant scope")
	moy := flags.String("moysklad-account", "", "registered MoySklad account")
	ozon := flags.String("ozon-account", "", "registered Ozon seller account")
	from := flags.String("from", "", "inclusive source updated time in RFC3339")
	to := flags.String("to", "", "exclusive source updated time in RFC3339")
	posting := flags.Int64("posting-observation", 0, "Ozon posting observation ID")
	batch := flags.Int64("order-batch", 0, "completed customer-order import batch ID")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
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
	if action == "correlate" {
		result, err := db.CorrelateCustomerOrder(ctx, postgres.OrderCorrelationScope{Tenant: *tenant, MoyskladAccount: *moy, OzonAccount: *ozon}, *posting, *batch)
		if err != nil {
			return err
		}
		return enc.Encode(result)
	}
	since, err := time.Parse(time.RFC3339Nano, *from)
	if err != nil {
		return errors.New("from must be RFC3339")
	}
	until, err := time.Parse(time.RFC3339Nano, *to)
	if err != nil {
		return errors.New("to must be RFC3339")
	}
	timeout, err := duration("MOYSKLAD_HTTP_TIMEOUT", 30*time.Second)
	if err != nil {
		return err
	}
	interval, err := duration("MOYSKLAD_REQUEST_INTERVAL", 400*time.Millisecond)
	if err != nil {
		return err
	}
	backoff, err := duration("MOYSKLAD_RETRY_BACKOFF", time.Second)
	if err != nil {
		return err
	}
	maxRetry, err := duration("MOYSKLAD_MAX_RETRY_AFTER", time.Minute)
	if err != nil {
		return err
	}
	attempts := 3
	if v := os.Getenv("MOYSKLAD_MAX_ATTEMPTS"); v != "" {
		attempts, err = strconv.Atoi(v)
		if err != nil {
			return errors.New("MOYSKLAD_MAX_ATTEMPTS must be an integer")
		}
	}
	client, err := moysklad.New(moysklad.Options{BaseURL: moysklad.BaseURL, Token: os.Getenv("MOYSKLAD_TOKEN"), Timeout: timeout, Interval: interval, Backoff: backoff, MaxRetryAfter: maxRetry, Attempts: attempts, PageSize: 100})
	if err != nil {
		return err
	}
	id, err := (application.CustomerOrderImporter{Reader: client, Store: db}).Run(ctx, *tenant, *moy, since, until)
	if err != nil {
		return err
	}
	return enc.Encode(struct{ BatchID int64 }{id})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "moysklad-orders:", err)
		os.Exit(1)
	}
}
