package main

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDisposableDatabaseSmoke(t *testing.T) {
	if os.Getenv("STYK_CHECKS_DOCKER_SMOKE") != "1" {
		t.Skip("explicit disposable Docker lifecycle smoke; included in full regression")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	env, cleanup, err := disposableDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	dsn := env[0][len("TEST_DATABASE_URL="):]
	if err = validateTestDatabase(dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("cannot open disposable PostgreSQL")
	}
	defer pool.Close()
	var value int
	pointURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid owned test DSN")
	}
	pointURL.Path = "/styk_orders_point_test"
	points, err := pgxpool.New(ctx, pointURL.String())
	if err != nil {
		t.Fatal("open companion native-point database")
	}
	defer points.Close()
	if err = points.QueryRow(ctx, "SELECT 1").Scan(&value); err != nil {
		t.Fatal("companion native-point database was not prepared")
	}
	if err = pool.QueryRow(ctx, "SELECT 1").Scan(&value); err != nil || value != 1 {
		t.Fatal("readiness returned before disposable PostgreSQL accepted TCP queries")
	}
}
