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

	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
)

type boundedReader struct {
	client *ozon.Client
	limits ozon.Limits
	counts ozon.Counts
}

func (r *boundedReader) ReadPostings(ctx context.Context, tenant string, since, until time.Time) (domain.PostingImport, error) {
	s, counts, err := r.client.ReadPostingsBounded(ctx, tenant, since, until, r.limits)
	r.counts = counts
	return s, err
}

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
	requests := flag.Int("max-requests", 100, "maximum logical API requests")
	attemptsLimit := flag.Int("max-attempts", 100, "maximum network attempts including retries")
	pages := flag.Int("max-pages", 100, "maximum listing pages")
	postings := flag.Int("max-postings", 1000, "maximum posting observations")
	bytes := flag.Int64("max-response-bytes", 4<<20, "maximum response bytes")
	maxDuration := flag.Duration("max-duration", 3*time.Minute, "maximum overall import duration")
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
	started := time.Now()
	if *maxDuration <= 0 {
		return errors.New("max-duration must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, *maxDuration)
	defer cancel()
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
	reader := &boundedReader{client: client, limits: ozon.Limits{Requests: *requests, Attempts: *attemptsLimit, Pages: *pages, Entities: 1, Products: 1, Warehouses: 1, Observations: *postings, Postings: *postings, ResponseBytes: *bytes, Duration: *maxDuration}}
	id, e := (application.PostingImporter{Reader: reader, Store: db}).Run(ctx, *tenant, start, end)
	result := ozon.Result(started, reader.counts, e)
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return err
	}
	if e != nil {
		return errors.New(string(result.Outcome))
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
