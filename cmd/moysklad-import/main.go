package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/postgres"
)

func duration(name string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return d, nil
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	token := os.Getenv("MOYSKLAD_TOKEN")
	account := strings.TrimSpace(os.Getenv("MOYSKLAD_ACCOUNT_ID"))
	if token == "" || account == "" {
		return errors.New("MOYSKLAD_TOKEN and MOYSKLAD_ACCOUNT_ID are required")
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
	client, err := moysklad.New(moysklad.Options{BaseURL: moysklad.BaseURL, Token: token, Timeout: timeout, Interval: interval, Backoff: backoff, MaxRetryAfter: maxRetry, Attempts: attempts, PageSize: 100})
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
	created, err := (application.Importer{Reader: client, Store: db}).Run(ctx, account)
	if err != nil {
		return err
	}
	if created {
		fmt.Println("import saved")
	} else {
		fmt.Println("identical import already saved")
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "import failed:", err)
		os.Exit(1)
	}
}
