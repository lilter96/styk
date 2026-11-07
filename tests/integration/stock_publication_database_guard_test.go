//go:build integration

package integration

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// This guard validates parsed connection destinations before any database call.
// Userinfo and query text can never stand in for the dedicated database path.
func publicationTestDatabaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "postgres" || u.Opaque != "" || u.Fragment != "" || u.RawFragment != "" || strings.Contains(raw, "#") || u.ForceQuery || (u.Path != "/styk_stock_publication_v1_test" && !ownedFullDatabasePath(u.Path)) || u.RawPath != "" {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if strings.ToLower(host) != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return false
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return false
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range query {
		if key != "sslmode" || len(values) != 1 {
			return false
		}
		switch values[0] {
		case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		default:
			return false
		}
	}
	return true
}

type publicationDatabaseURLCase struct {
	name, raw string
	valid     bool
}

func publicationDatabaseURLCases() []publicationDatabaseURLCase {
	const path = "/styk_stock_publication_v1_test"
	return []publicationDatabaseURLCase{
		{"dedicated-private-shape", "postgres://synthetic:dummy-password@127.0.0.1:5432" + path + "?sslmode=disable", true},
		{"localhost", "postgres://synthetic@localhost" + path, true},
		{"ipv6-loopback", "postgres://synthetic@[::1]:5432" + path + "?sslmode=require", true},
		{"owned-full-worker", "postgres://synthetic@127.0.0.1/styk_full_123_456789_7_test?sslmode=disable", true},
		{"malformed-full-worker", "postgres://synthetic@127.0.0.1/styk_full_123_456789_8_test", false},
		{"release-database-stays-skipped", "postgres://synthetic@127.0.0.1/styk_orders_release_test", false},
		{"protected-db-testname-in-password", "postgres://synthetic:styk_stock_publication_v1_test@127.0.0.1/styk", false},
		{"protected-db-testname-in-query", "postgres://synthetic@127.0.0.1/styk?application_name=styk_stock_publication_v1_test", false},
		{"dbname-query-override", "postgres://synthetic@127.0.0.1" + path + "?dbname=styk", false},
		{"database-query-override", "postgres://synthetic@127.0.0.1" + path + "?database=styk", false},
		{"hostaddr-query-override", "postgres://synthetic@127.0.0.1" + path + "?hostaddr=192.0.2.1", false},
		{"host-query-override", "postgres://synthetic@127.0.0.1" + path + "?host=192.0.2.1", false},
		{"service-query-override", "postgres://synthetic@127.0.0.1" + path + "?service=production", false},
		{"search-path-query-override", "postgres://synthetic@127.0.0.1" + path + "?search_path=public", false},
		{"encoded-query-override", "postgres://synthetic@127.0.0.1" + path + "?%64bname=styk", false},
		{"remote-host", "postgres://synthetic@192.0.2.1" + path, false},
		{"loopback-name-suffix", "postgres://synthetic@127.0.0.1.example.invalid" + path, false},
		{"unix-socket-override", "postgres://synthetic" + path + "?host=/var/run/postgresql", false},
		{"multiple-hosts", "postgres://synthetic@127.0.0.1,192.0.2.1" + path, false},
		{"wrong-scheme", "postgresql://synthetic@127.0.0.1" + path, false},
		{"opaque-url", "postgres:127.0.0.1" + path, false},
		{"fragment", "postgres://synthetic@127.0.0.1" + path + "#test", false},
		{"empty-fragment", "postgres://synthetic@127.0.0.1" + path + "#", false},
		{"duplicate-sslmode", "postgres://synthetic@127.0.0.1" + path + "?sslmode=disable&sslmode=require", false},
		{"empty-sslmode", "postgres://synthetic@127.0.0.1" + path + "?sslmode=", false},
		{"unknown-sslmode", "postgres://synthetic@127.0.0.1" + path + "?sslmode=unknown", false},
		{"malformed-query", "postgres://synthetic@127.0.0.1" + path + "?sslmode=%xx", false},
		{"trailing-query", "postgres://synthetic@127.0.0.1" + path + "?", false},
		{"wrong-path-suffix", "postgres://synthetic@127.0.0.1" + path + "_copy", false},
		{"path-escape", "postgres://synthetic@127.0.0.1" + path + "/../styk", false},
		{"bad-port", "postgres://synthetic@127.0.0.1:99999" + path, false},
		{"zero-port", "postgres://synthetic@127.0.0.1:0" + path, false},
		{"empty", "", false},
	}
}

func TestPublicationTestDatabaseURLPinsParsedLoopbackDestination(t *testing.T) {
	t.Parallel()
	for _, test := range publicationDatabaseURLCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := publicationTestDatabaseURL(test.raw); got != test.valid {
				t.Fatalf("parsed destination accepted=%v, want=%v", got, test.valid)
			}
		})
	}
}
