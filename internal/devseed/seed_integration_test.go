//go:build integration

package devseed

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lilter96/styk/internal/identity"
)

func TestSeedRollbackConcurrentIdempotenceAndIsolation(t *testing.T) {
	url := os.Getenv("TEST_DEMO_DATABASE_URL")
	if url == "" {
		t.Skip("requires a disposable separately hosted styk_demo_dev database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool := demoTestPool(t, ctx, url)
	demoTestOwner(t, ctx, pool)
	c := Config{Environment: "development", URL: url, Seed: 42, AsOf: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	assertRollback(t, ctx, pool, c)
	assertConcurrentSeed(t, ctx, pool, c)
	changed := c
	changed.Seed++
	if _, err := Run(ctx, pool, changed); err == nil {
		t.Fatal("different seed silently replaced immutable evidence")
	}
	store, err := identity.New(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Register(ctx, "other@example.test", demoTestPassword(t), "Other tenant"); err != nil {
		t.Fatal(err)
	}
	if _, err = Run(ctx, pool, c); err == nil {
		t.Fatal("seed accepted another user's data")
	}
}

func demoTestPool(t *testing.T, ctx context.Context, url string) *pgxpool.Pool {
	t.Helper()
	if err := (Config{Environment: "development", URL: url, Seed: 42, AsOf: time.Now().UTC().Truncate(time.Second)}).Validate(); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var host, database, marker string
	if err = pool.QueryRow(ctx, `SELECT inet_server_addr()::text,current_database(),coalesce(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=current_database()`).Scan(&host, &database, &marker); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(url, "127.0.0.1") || database != Database || marker != Marker {
		t.Fatal("unmarked or remote test database")
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM saas_users`).Scan(&count); err != nil || count != 0 {
		t.Fatal("test requires an empty disposable database")
	}
	return pool
}

func demoTestPassword(t *testing.T) string {
	t.Helper()
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(value[:])
}

func demoTestOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	store, err := identity.New(pool, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	password := demoTestPassword(t)
	if _, _, err = store.Register(ctx, "demo@styk.local", password, "Demo"); err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateVerification(ctx, "demo@styk.local")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.VerifyEmail(ctx, token, password); err != nil {
		t.Fatal(err)
	}
}

func assertRollback(t *testing.T, ctx context.Context, pool *pgxpool.Pool, c Config) {
	t.Helper()
	if _, err := pool.Exec(ctx, `ALTER TABLE ozon_posting_lines ADD CONSTRAINT devseed_forced_failure CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, pool, c); err == nil {
		t.Fatal("fixture failure was ignored")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM source_imports`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed seed leaked partial imports")
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE ozon_posting_lines DROP CONSTRAINT devseed_forced_failure`); err != nil {
		t.Fatal(err)
	}
}

func assertConcurrentSeed(t *testing.T, ctx context.Context, pool *pgxpool.Pool, c Config) {
	t.Helper()
	type result struct {
		report Report
		err    error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() { r, err := Run(ctx, pool, c); results <- result{r, err} }()
	}
	created := 0
	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !r.report.Existing {
			created++
		}
	}
	if created != 1 {
		t.Fatal("concurrent seed duplicated work")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ozon_product_observations`).Scan(&count); err != nil || count != 2500 {
		t.Fatal("seed count mismatch")
	}
}
