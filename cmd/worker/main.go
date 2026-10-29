package main

import (
	"context"
	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/postgres"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

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
	return (application.Worker{Store: db, Handler: application.MockHandler{}, Log: slog.Default(), Poll: c.PollInterval, Lease: c.LeaseDuration, Retry: c.RetryDelay}).Run(ctx)
}
