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
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/identity"
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
	handler, err := configuredHandler(db, c.HTTPAddr)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: c.HTTPAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
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

func configuredHandler(db *postgres.Store, address string) (http.Handler, error) {
	mode := os.Getenv("STYK_AUTH_MODE")
	if mode == "" {
		mode = "merchant"
	}
	if mode == "disabled" {
		if err := console.ValidateAddress(address); err != nil && os.Getenv("STYK_ALLOW_REMOTE_BIND") != "true" {
			return nil, errors.New("non-loopback health bind requires STYK_ALLOW_REMOTE_BIND=true")
		}
		return transport.Handler(db), nil
	}
	if err := console.ValidateAddress(address); err != nil {
		return nil, errors.New("authenticated API must bind to loopback behind a trusted proxy")
	}
	api, err := apiForMode(db, mode)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", api)
	mux.Handle("/", transport.Handler(db))
	return mux, nil
}

func apiForMode(db *postgres.Store, mode string) (http.Handler, error) {
	origin := os.Getenv("STYK_CONSOLE_ORIGIN")
	if mode == "local" {
		if origin == "" {
			origin = "http://127.0.0.1:5173"
		}
		return console.Handler(db, os.Getenv("STYK_OPERATOR_SECRET"), origin)
	}
	if mode != "merchant" {
		return nil, errors.New("STYK_AUTH_MODE must be merchant, local or disabled")
	}
	vault, err := credentials.FromEnvironment()
	if err != nil || vault == nil {
		return nil, errors.New("merchant mode requires a valid credential encryption key")
	}
	sender, err := identity.NewSMTP(os.Getenv("STYK_SMTP_ADDRESS"), os.Getenv("STYK_SMTP_FROM"), os.Getenv("STYK_SMTP_USERNAME"), os.Getenv("STYK_SMTP_PASSWORD"))
	if err != nil {
		return nil, errors.New("merchant mode requires configured SMTP verification and recovery delivery")
	}
	return console.MerchantHandler(db, origin, sender)
}
