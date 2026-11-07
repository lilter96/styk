package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var businessDatabaseName = regexp.MustCompile(`^styk_full_[1-9][0-9]*_[0-9]+_[0-7]_test$`)

func ownedBusinessDatabase(name string) bool {
	return len(name) <= 63 && businessDatabaseName.MatchString(name)
}

func businessDatabases() []string {
	names := make([]string, businessWorkers)
	for i := range names {
		names[i] = fmt.Sprintf("styk_full_%d_%d_%d_test", os.Getpid(), time.Now().UnixNano(), i)
	}
	return names
}

func prepareBusinessDatabase(ctx context.Context, root string, env []string, name string) ([]string, error) {
	isolated, err := businessDatabaseEnvironment(env, name)
	if err != nil {
		return nil, err
	}
	dsn := strings.TrimPrefix(env[0], "TEST_DATABASE_URL=")
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open business database connection: %w", err)
	}
	_, err = pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	pool.Close()
	if err != nil {
		return nil, fmt.Errorf("create isolated business database: %w", err)
	}
	// Only an internally named database just created on the validated loopback
	// server reaches this path; the public supplied-DSN allowlist stays strict.
	dsn = strings.TrimPrefix(isolated[0], "TEST_DATABASE_URL=")
	if err = command(ctx, root, isolated, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "migrations", "postgres", dsn, "up"); err != nil {
		return nil, err
	}
	return isolated, nil
}

func businessDatabaseEnvironment(env []string, name string) ([]string, error) {
	dsn := strings.TrimPrefix(env[0], "TEST_DATABASE_URL=")
	if err := validateTestDatabase(dsn); err != nil {
		return nil, err
	}
	if !ownedBusinessDatabase(name) {
		return nil, fmt.Errorf("refusing an unowned business database name")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dedicated business database: %w", err)
	}
	query := u.Query()
	query.Del("search_path")
	u.Path = "/" + name
	u.RawQuery = query.Encode()
	isolated := append(databaseEnvironment(u.String()), env[2:]...)
	return isolated, nil
}

func removeBusinessDatabases(env, names []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, strings.TrimPrefix(env[0], "TEST_DATABASE_URL="))
	if err != nil {
		return fmt.Errorf("open business database cleanup: %w", err)
	}
	defer pool.Close()
	for _, name := range names {
		if !ownedBusinessDatabase(name) {
			return fmt.Errorf("refusing to remove an unowned business database")
		}
		if _, err = pool.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			return fmt.Errorf("remove owned business database: %w", err)
		}
	}
	return nil
}
