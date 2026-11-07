package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestOwnedBusinessDatabaseDerivationKeepsPublicDSNAllowlistStrict(t *testing.T) {
	t.Parallel()
	base := databaseEnvironment("postgres://synthetic@127.0.0.1/styk_orders_store_test?sslmode=disable&search_path=old_schema")
	env, err := businessDatabaseEnvironment(base, "styk_full_123_456_0_test")
	if err != nil {
		t.Fatal(err)
	}
	dsn := strings.TrimPrefix(env[0], "TEST_DATABASE_URL=")
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Path != "/styk_full_123_456_0_test" || u.Query().Get("search_path") != "" {
		t.Fatal("owned worker escaped the dedicated server or inherited a shared schema")
	}
	if validateTestDatabase(dsn) == nil {
		t.Fatal("public supplied-DSN validation now accepts internally owned worker names")
	}
	for _, name := range []string{"styk_orders_store_test", "styk_full_0_456_0_test", "styk_full_123_456_8_test", "styk_full_123_456_0_test;DROP DATABASE other", "styk_full_123_" + strings.Repeat("1", 64) + "_0_test"} {
		if _, err = businessDatabaseEnvironment(base, name); err == nil {
			t.Fatalf("unowned worker database name accepted: %s", name)
		}
	}
	remote := databaseEnvironment("postgres://synthetic@example.com/styk_orders_store_test")
	if _, err = businessDatabaseEnvironment(remote, "styk_full_123_456_0_test"); err == nil {
		t.Fatal("owned worker derivation accepted a remote base server")
	}
}
