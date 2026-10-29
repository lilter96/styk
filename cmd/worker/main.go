package main

import (
	"context"
	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/ozonjob"
	"github.com/lilter96/styk/internal/postgres"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// catalogClient preserves stricter local Ozon settings while capping per-request
// duration and retries for operator-triggered jobs. Invalid settings leave the
// account unclaimed until the worker is configured correctly.
func catalogClient(account, key string) *ozon.Client {
	if account == "" || key == "" {
		return nil
	}
	duration := func(name string, fallback time.Duration) (time.Duration, bool) {
		raw := os.Getenv(name)
		if raw == "" {
			return fallback, true
		}
		d, err := time.ParseDuration(raw)
		return d, err == nil && d > 0
	}
	timeout, ok := duration("OZON_HTTP_TIMEOUT", 30*time.Second)
	if !ok {
		return nil
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	interval, ok := duration("OZON_REQUEST_INTERVAL", 400*time.Millisecond)
	if !ok {
		return nil
	}
	if interval < 400*time.Millisecond {
		interval = 400 * time.Millisecond
	}
	backoff, ok := duration("OZON_RETRY_BACKOFF", time.Second)
	if !ok {
		return nil
	}
	if backoff < time.Second {
		backoff = time.Second
	}
	if backoff > 30*time.Second {
		return nil
	}
	maxRetry, ok := duration("OZON_MAX_RETRY_AFTER", time.Minute)
	if !ok {
		return nil
	}
	if maxRetry > time.Minute {
		maxRetry = time.Minute
	}
	attempts := 3
	if raw := os.Getenv("OZON_MAX_ATTEMPTS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 10 {
			return nil
		}
		if n < attempts {
			attempts = n
		}
	}
	client, _ := ozon.New(ozon.Options{BaseURL: ozon.BaseURL, ClientID: account, APIKey: key, Timeout: timeout, Interval: interval, Backoff: backoff, MaxRetryAfter: maxRetry, Attempts: attempts, PageSize: 100})
	return client
}

func moyskladCatalogClient(account, key string) *moysklad.Client {
	if account == "" || key == "" {
		return nil
	}
	duration := func(name string, fallback time.Duration) (time.Duration, bool) {
		raw := os.Getenv(name)
		if raw == "" {
			return fallback, true
		}
		d, err := time.ParseDuration(raw)
		return d, err == nil && d > 0
	}
	timeout, ok := duration("MOYSKLAD_HTTP_TIMEOUT", 30*time.Second)
	if !ok {
		return nil
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	interval, ok := duration("MOYSKLAD_REQUEST_INTERVAL", 400*time.Millisecond)
	if !ok {
		return nil
	}
	if interval < 400*time.Millisecond {
		interval = 400 * time.Millisecond
	}
	backoff, ok := duration("MOYSKLAD_RETRY_BACKOFF", time.Second)
	if !ok {
		return nil
	}
	if backoff < time.Second {
		backoff = time.Second
	}
	if backoff > 30*time.Second {
		return nil
	}
	maxRetry, ok := duration("MOYSKLAD_MAX_RETRY_AFTER", time.Minute)
	if !ok {
		return nil
	}
	if maxRetry > time.Minute {
		maxRetry = time.Minute
	}
	attempts := 3
	if raw := os.Getenv("MOYSKLAD_MAX_ATTEMPTS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 10 {
			return nil
		}
		if n < attempts {
			attempts = n
		}
	}
	client, _ := moysklad.New(moysklad.Options{BaseURL: moysklad.BaseURL, Token: key, Timeout: timeout, Interval: interval, Backoff: backoff, MaxRetryAfter: maxRetry, Attempts: attempts, PageSize: 100})
	return client
}

func main() {
	if err := run(); err != nil {
		slog.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := postgres.Open(ctx, c.DatabaseURL, c.QueryTimeout)
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	account := os.Getenv("OZON_CLIENT_ID")
	key := os.Getenv("OZON_API_KEY")
	client := catalogClient(account, key)
	claimAccount := account
	if client == nil {
		claimAccount = ""
	}
	moyAccount := os.Getenv("MOYSKLAD_ACCOUNT_ID")
	moyClient := moyskladCatalogClient(moyAccount, os.Getenv("MOYSKLAD_TOKEN"))
	moyClaim := moyAccount
	if moyClient == nil {
		moyClaim = ""
	}
	return (application.Worker{MoyskladImportAccount: moyClaim, Store: db, Handler: application.MockHandler{}, Imports: ozonjob.Runner{Store: db, Account: account, Client: client, MoyskladAccount: moyAccount, MoyskladClient: moyClient}, ImportAccount: claimAccount, Log: slog.Default(), Poll: c.PollInterval, Lease: c.LeaseDuration, Retry: c.RetryDelay}).Run(ctx)
}
