package main

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/styk/internal/application"
	"example.com/styk/internal/config"
	"example.com/styk/internal/domain"
	"example.com/styk/internal/infrastructure/ozon"
	"example.com/styk/internal/postgres"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

type boundedReader struct {
	client *ozon.Client
	limits ozon.Limits
	counts ozon.Counts
}

func (r *boundedReader) Read(ctx context.Context) (domain.OzonImport, error) {
	s, counts, err := r.client.ReadBounded(ctx, r.limits)
	r.counts = counts
	return s, err
}

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
	tenant := flag.String("tenant", "", "registered tenant")
	requests := flag.Int("max-requests", 100, "maximum logical API requests")
	attemptsLimit := flag.Int("max-attempts", 100, "maximum network attempts including retries")
	pages := flag.Int("max-pages", 100, "maximum API pages")
	products := flag.Int("max-products", 1000, "maximum products")
	warehouses := flag.Int("max-warehouses", 1000, "maximum warehouses")
	stocks := flag.Int("max-stock", 10000, "maximum stock observations")
	bytes := flag.Int64("max-response-bytes", 4<<20, "maximum response bytes")
	limitDuration := flag.Duration("max-duration", 3*time.Minute, "maximum overall import duration")
	flag.Parse()
	if flag.NArg() != 0 || *tenant == "" || *products < 1 || *warehouses < 1 || *products > 200000 || *warehouses > 200000 {
		return errors.New("tenant and valid bounded limits are required")
	}
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
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, *limitDuration)
	defer cancel()
	db, err := postgres.Open(ctx, cfg.DatabaseURL, cfg.QueryTimeout)
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	var registered bool
	registrationCtx, cancelRegistration := context.WithTimeout(ctx, cfg.QueryTimeout)
	err = db.Pool.QueryRow(registrationCtx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='ozon' AND account_id=$2)`, *tenant, id).Scan(&registered)
	cancelRegistration()
	if err != nil || !registered {
		return errors.New("Ozon account is not registered to tenant")
	}
	reader := &boundedReader{client: c, limits: ozon.Limits{Requests: *requests, Attempts: *attemptsLimit, Pages: *pages, Entities: *products + *warehouses, Products: *products, Warehouses: *warehouses, Observations: *stocks, Postings: 1, ResponseBytes: *bytes, Duration: *limitDuration}}
	created, err := (application.OzonImporter{Reader: reader, Store: db}).Run(ctx)
	result := ozon.Result(started, reader.counts, err)
	if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
		return encodeErr
	}
	if err != nil {
		return errors.New(string(result.Outcome))
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
