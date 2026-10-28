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

	"example.com/styk/internal/doctor"
	"example.com/styk/internal/doctordemo"
	"example.com/styk/internal/infrastructure/moysklad"
	"example.com/styk/internal/infrastructure/ozon"
	"example.com/styk/internal/postgres"
)

func run() error {
	f := flag.NewFlagSet("doctor", flag.ContinueOnError)
	tenant := f.String("tenant", "", "registered tenant")
	moy := f.String("moysklad-account", "", "registered account")
	oz := f.String("ozon-account", "", "registered seller account")
	live := f.Bool("live", false, "explicitly authorize bounded read-only requests for selected accounts")
	requestTimeout := f.Duration("request-timeout", 5*time.Second, "per-request live timeout; one attempt per source")
	demo := f.Bool("demo", false, "seed a fresh synthetic namespace in a disposable migrated database with synthetic evidence")
	format := f.String("format", "text", "text or json")
	age := f.Duration("max-age", time.Hour, "maximum local observation age")
	timeout := f.Duration("timeout", 30*time.Second, "total diagnostic time limit")
	if err := f.Parse(os.Args[1:]); err != nil {
		return err
	}
	if (*live && *demo) || *requestTimeout <= 0 {
		return errors.New("live and demo are exclusive; request timeout must be positive")
	}
	if f.NArg() != 0 || (*format != "text" && *format != "json") || *age <= 0 || *timeout <= 0 {
		return errors.New("invalid diagnostic settings")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	s, err := postgres.Open(ctx, os.Getenv("DATABASE_URL"), *timeout)
	if err != nil {
		return errors.New("cannot open diagnostic database")
	}
	defer s.Pool.Close()
	scope := postgres.MatchScope{Tenant: *tenant, MoyskladAccount: *moy, OzonAccount: *oz}
	now := time.Now().UTC()
	if *demo {
		scope, err = doctordemo.Run(ctx, s)
		if err != nil {
			return err
		}
		now = time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	}
	r, err := s.Doctor(ctx, scope, now, *age)
	if err != nil {
		return errors.New("diagnostic evidence read failed; verify scope, migrations and database availability")
	}
	if *live {
		// Scope registration must pass before any credential is used.
		registered := false
		for _, c := range r.Checks {
			if c.Code == "ACCOUNT_REGISTRATION" && c.Status == doctor.Pass {
				registered = true
			}
		}
		if !registered {
			return errors.New("live diagnostics require both registered accounts")
		}
		var mp, op doctor.Probe
		if token := os.Getenv("MOYSKLAD_TOKEN"); token != "" {
			c, e := moysklad.New(moysklad.Options{BaseURL: moysklad.BaseURL, Token: token, Timeout: *requestTimeout, Interval: time.Second, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
			if e != nil {
				return errors.New("invalid MoySklad credentials or settings")
			}
			mp = func(ctx context.Context) (bool, error) { return c.Probe(ctx, scope.MoyskladAccount) }
		}
		if key := os.Getenv("OZON_API_KEY"); key != "" && os.Getenv("OZON_CLIENT_ID") == scope.OzonAccount {
			c, e := ozon.New(ozon.Options{BaseURL: ozon.BaseURL, ClientID: scope.OzonAccount, APIKey: key, Timeout: *requestTimeout, Interval: time.Second, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
			if e != nil {
				return errors.New("invalid Ozon credentials or settings")
			}
			op = func(ctx context.Context) (bool, error) { err := c.Probe(ctx); return err == nil, err }
		}
		doctor.Live(ctx, &r, mp, op)
	}
	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	fmt.Println("STYK Integration Doctor")
	for _, c := range r.Checks {
		fmt.Printf("%s: %s — %s\n", c.Code, c.Status, c.Detail)
	}
	b, _ := json.Marshal(r.Batches)
	fmt.Printf("Import evidence: %s\n", b)
	m, _ := json.Marshal(r.MappingCounts)
	c, _ := json.Marshal(r.CorrelationStatuses)
	fmt.Printf("Mappings: %s\nCorrelations: %s; truncated=%t\n", m, c, r.CorrelationsTruncated)
	fmt.Printf("Stock calculation: %s\nReasons: %v\nExternal stock publication: %s\n", r.DecisionStatus, r.DecisionReasons, r.Publication)
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "doctor:", err)
		os.Exit(1)
	}
}
