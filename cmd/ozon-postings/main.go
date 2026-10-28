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
	"strings"
	"syscall"
	"time"

	"example.com/styk/internal/application"
	"example.com/styk/internal/config"
	"example.com/styk/internal/domain"
	"example.com/styk/internal/infrastructure/ozon"
	"example.com/styk/internal/postgres"
)

func duration(name string, fallback time.Duration) (time.Duration, error) {
	if value := os.Getenv(name); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("%s must be a positive duration", name)
		}
		return d, nil
	}
	return fallback, nil
}

func run() error {
	tenant := flag.String("tenant", "", "tenant ID")
	from := flag.String("from", "", "RFC3339 start")
	to := flag.String("to", "", "RFC3339 end")
	inspect := flag.Bool("inspect", false, "read stored observations and current correlations")
	moy := flag.String("moysklad-account", "", "MoySklad account for inspection")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if strings.TrimSpace(*tenant) == "" {
		return errors.New("tenant is required")
	}
	cfg, e := config.Load()
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, e := postgres.Open(ctx, cfg.DatabaseURL, cfg.QueryTimeout)
	if e != nil {
		return e
	}
	defer db.Pool.Close()
	if *inspect {
		rows, e := db.ListPostings(ctx, domain.PostingReadScope{Tenant: *tenant, Account: os.Getenv("OZON_CLIENT_ID"), MoyskladAccount: *moy})
		if e != nil {
			return e
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(rows)
	}
	start, e := time.Parse(time.RFC3339, *from)
	if e != nil {
		return errors.New("invalid from")
	}
	end, e := time.Parse(time.RFC3339, *to)
	if e != nil {
		return errors.New("invalid to")
	}
	timeout, e := duration("OZON_HTTP_TIMEOUT", 30*time.Second)
	if e != nil {
		return e
	}
	interval, e := duration("OZON_REQUEST_INTERVAL", 400*time.Millisecond)
	if e != nil {
		return e
	}
	backoff, e := duration("OZON_RETRY_BACKOFF", time.Second)
	if e != nil {
		return e
	}
	maxRetry, e := duration("OZON_MAX_RETRY_AFTER", time.Minute)
	if e != nil {
		return e
	}
	attempts := 3
	if v := os.Getenv("OZON_MAX_ATTEMPTS"); v != "" {
		attempts, e = strconv.Atoi(v)
		if e != nil {
			return errors.New("OZON_MAX_ATTEMPTS must be an integer")
		}
	}
	client, e := ozon.New(ozon.Options{BaseURL: ozon.BaseURL, ClientID: os.Getenv("OZON_CLIENT_ID"), APIKey: os.Getenv("OZON_API_KEY"), Timeout: timeout, Interval: interval, Backoff: backoff, MaxRetryAfter: maxRetry, Attempts: attempts, PageSize: 100})
	if e != nil {
		return e
	}
	id, e := (application.PostingImporter{Reader: client, Store: db}).Run(ctx, *tenant, start, end)
	if e != nil {
		return e
	}
	fmt.Printf("saved batch %d\n", id)
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "postings:", e)
		os.Exit(1)
	}
}
