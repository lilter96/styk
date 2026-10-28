package main

import (
	"context"
	"errors"
	"example.com/styk/internal/application"
	"example.com/styk/internal/config"
	"example.com/styk/internal/infrastructure/ozon"
	"example.com/styk/internal/postgres"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func duration(name string, def time.Duration) (time.Duration, error) {
	if v := os.Getenv(name); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("%s must be a positive duration", name)
		}
		return d, nil
	}
	return def, nil
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	id, key := os.Getenv("OZON_CLIENT_ID"), os.Getenv("OZON_API_KEY")
	if id == "" || key == "" {
		return errors.New("OZON_CLIENT_ID and OZON_API_KEY are required")
	}
	timeout, err := duration("OZON_HTTP_TIMEOUT", 30*time.Second)
	if err != nil {
		return err
	}
	interval, err := duration("OZON_REQUEST_INTERVAL", 400*time.Millisecond)
	if err != nil {
		return err
	}
	backoff, err := duration("OZON_RETRY_BACKOFF", time.Second)
	if err != nil {
		return err
	}
	maxRetry, err := duration("OZON_MAX_RETRY_AFTER", time.Minute)
	if err != nil {
		return err
	}
	attempts := 3
	if v := os.Getenv("OZON_MAX_ATTEMPTS"); v != "" {
		attempts, err = strconv.Atoi(v)
		if err != nil {
			return errors.New("OZON_MAX_ATTEMPTS must be an integer")
		}
	}
	c, err := ozon.New(ozon.Options{BaseURL: ozon.BaseURL, ClientID: id, APIKey: key, Timeout: timeout, Interval: interval, Backoff: backoff, MaxRetryAfter: maxRetry, Attempts: attempts, PageSize: 100})
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
	created, err := (application.OzonImporter{Reader: c, Store: db}).Run(ctx)
	if err != nil {
		return err
	}
	if created {
		fmt.Println("Ozon import saved")
	} else {
		fmt.Println("identical Ozon import already saved")
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "import failed:", err)
		os.Exit(1)
	}
}
