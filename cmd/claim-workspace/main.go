// Command claim-workspace assigns an existing unclaimed local workspace to a
// registered user. It is never exposed through the HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	workspace := flag.String("tenant", "", "existing unclaimed workspace identifier")
	userID := flag.String("user-id", "", "registered user identifier")
	flag.Parse()
	if os.Getenv("STYK_LOCAL_CLAIM_ENABLE") != "true" || *workspace == "" || *userID == "" {
		return errors.New("local claim requires STYK_LOCAL_CLAIM_ENABLE=true, -tenant and -user-id")
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	if !localDatabase(c.DatabaseURL) {
		return errors.New("legacy claim requires a loopback PostgreSQL host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, c.DatabaseURL, c.QueryTimeout)
	if err != nil {
		return errors.New("cannot open local database")
	}
	defer db.Pool.Close()
	service, err := identity.New(db.Pool, c.QueryTimeout)
	if err != nil {
		return err
	}
	if err = service.ClaimLegacy(ctx, *workspace, *userID); err != nil {
		return fmt.Errorf("claim workspace: %w", err)
	}
	fmt.Println("Existing workspace claimed; history remains unchanged.")
	return nil
}

func localDatabase(raw string) bool {
	c, err := pgx.ParseConfig(raw)
	if err != nil || !loopbackDatabaseHost(c.Host) {
		return false
	}
	for _, fallback := range c.Fallbacks {
		if !loopbackDatabaseHost(fallback.Host) {
			return false
		}
	}
	return true
}

func loopbackDatabaseHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
