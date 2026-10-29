package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/console"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/transport"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api stopped", "error", err)
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
	if err = console.ValidateAddress(c.HTTPAddr); err != nil && os.Getenv("STYK_ALLOW_REMOTE_BIND") != "true" {
		return errors.New("non-loopback API bind requires STYK_ALLOW_REMOTE_BIND=true")
	}
	handler := transport.Handler(db)
	if secret := os.Getenv("STYK_OPERATOR_SECRET"); secret != "" {
		if err = console.ValidateAddress(c.HTTPAddr); err != nil {
			return err
		}
		origin := os.Getenv("STYK_CONSOLE_ORIGIN")
		if origin == "" {
			origin = "http://127.0.0.1:5173"
		}
		api, e := console.Handler(db, secret, origin)
		if e != nil {
			return e
		}
		mux := http.NewServeMux()
		mux.Handle("/api/", api)
		mux.Handle("/", handler)
		handler = mux
	}
	srv := &http.Server{Addr: c.HTTPAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	select {
	case err = <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
		defer cancel()
		if err = srv.Shutdown(shutdown); err != nil {
			return err
		}
		err = <-done
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
