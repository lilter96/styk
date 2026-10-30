package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lilter96/styk/internal/devseed"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	seed := flag.Int64("seed", 42, "deterministic nonnegative fixture seed")
	asOf := flag.String("as-of", "", "required RFC3339 reference time")
	flag.Parse()
	timestamp, err := time.Parse(time.RFC3339, *asOf)
	if err != nil {
		return fmt.Errorf("parse explicit as-of: %w", err)
	}
	c := devseed.Config{Environment: os.Getenv("APP_ENV"), URL: os.Getenv("DATABASE_URL"), Seed: *seed, AsOf: timestamp}
	if err = c.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, c.URL)
	if err != nil {
		return fmt.Errorf("configure demo connection: %w", err)
	}
	defer pool.Close()
	report, err := devseed.Run(ctx, pool, c)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
