package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const testDatabaseName = "styk_orders_store_test"

func testDatabase(ctx context.Context) ([]string, func(), error) {
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		if err := validateTestDatabase(dsn); err != nil {
			return nil, nil, err
		}
		return databaseEnvironment(dsn), func() {}, nil
	}
	return disposableDatabase(ctx)
}

func validateTestDatabase(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !testHost(u.Hostname()) || u.Fragment != "" {
		return errors.New("TEST_DATABASE_URL must be a dedicated loopback PostgreSQL URL")
	}
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil || !testHost(c.ConnConfig.Host) || !testName(c.ConnConfig.Database) {
		return errors.New("TEST_DATABASE_URL cannot override the dedicated loopback host or test database")
	}
	if strings.TrimPrefix(u.Path, "/") != c.ConnConfig.Database {
		return errors.New("TEST_DATABASE_URL cannot override the database in its query")
	}
	for _, fallback := range c.ConnConfig.Fallbacks {
		if !testHost(fallback.Host) {
			return errors.New("TEST_DATABASE_URL cannot use a remote fallback")
		}
	}
	return nil
}

func testHost(host string) bool { return host == "127.0.0.1" || host == "localhost" }
func testName(name string) bool {
	return name == testDatabaseName || name == "styk_orders_release_test"
}

func databaseEnvironment(dsn string) []string {
	return []string{"TEST_DATABASE_URL=" + dsn, "DATABASE_URL=" + dsn}
}

func disposableDatabase(ctx context.Context) ([]string, func(), error) {
	name, password := "styk-checks-"+rand.Text()[:12], rand.Text()
	cleanup := func() { removeDatabase(name) }
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--detach", "--name", name, "--publish", "127.0.0.1::5432", "--env", "POSTGRES_USER=styk", "--env", "POSTGRES_PASSWORD", "--env", "POSTGRES_DB="+testDatabaseName, "postgres:17.6")
	cmd.Env = append(cleanEnvironment(), "POSTGRES_PASSWORD="+password)
	if err := cmd.Run(); err != nil {
		cleanup()
		return nil, nil, errors.New("cannot start disposable PostgreSQL; start Docker or supply a dedicated TEST_DATABASE_URL")
	}
	fmt.Println("Started disposable PostgreSQL on a random loopback port.")
	dsn, err := databaseAddress(ctx, name, password)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if err = awaitDatabase(ctx, name); err != nil {
		cleanup()
		return nil, nil, err
	}
	if err = prepareDisposablePointDatabase(ctx, name); err != nil {
		cleanup()
		return nil, nil, err
	}
	return databaseEnvironment(dsn), cleanup, nil
}

// Native-point contracts deliberately use a second dedicated database on the
// same disposable server; they must never fall back to a developer database.
func prepareDisposablePointDatabase(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "docker", "exec", name, "createdb", "-U", "styk", "styk_orders_point_test")
	cmd.Env = cleanEnvironment()
	if err := cmd.Run(); err != nil {
		return errors.New("cannot prepare disposable native-point test database")
	}
	return nil
}

func databaseAddress(ctx context.Context, name, password string) (string, error) {
	data, err := output(ctx, "", "docker", "port", name, "5432/tcp")
	if err != nil {
		return "", err
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(string(data)))
	if err != nil || host != "127.0.0.1" {
		return "", errors.New("disposable PostgreSQL must bind only to loopback")
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword("styk", password), Host: net.JoinHostPort(host, port), Path: "/" + testDatabaseName, RawQuery: "sslmode=disable"}
	return u.String(), nil
}

func awaitDatabase(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		cmd := exec.CommandContext(ctx, "docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "styk", "-d", testDatabaseName)
		cmd.Env = cleanEnvironment()
		if err := cmd.Run(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("disposable PostgreSQL readiness deadline exceeded")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func removeDatabase(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "rm", "--force", name)
	cmd.Env = cleanEnvironment()
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Could not remove owned test container:", name)
	}
}

func migrateTestDatabase(ctx context.Context, root string, env []string) error {
	dsn := strings.TrimPrefix(env[0], "TEST_DATABASE_URL=")
	if err := validateTestDatabase(dsn); err != nil {
		return err
	}
	return command(ctx, root, env, "go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "migrations", "postgres", dsn, "up")
}
