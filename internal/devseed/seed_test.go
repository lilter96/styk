package devseed

import (
	"testing"
	"time"
)

func TestDevelopmentBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, environment, url string
		seed                   int64
		valid                  bool
	}{
		{"local demo", "development", "postgres://localhost/styk_demo_dev", 42, true},
		{"production", "production", "postgres://localhost/styk_demo_dev", 42, false},
		{"remote", "development", "postgres://example.invalid/styk_demo_dev", 42, false},
		{"wrong database", "development", "postgres://localhost/styk", 42, false},
		{"routing override", "development", "postgres://localhost/styk_demo_dev?host=example.invalid", 42, false},
		{"negative seed", "development", "postgres://localhost/styk_demo_dev", -1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := Config{Environment: tc.environment, URL: tc.url, Seed: tc.seed, AsOf: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)}
			if (c.Validate() == nil) != tc.valid {
				t.Fatal("incorrect development boundary")
			}
		})
	}
}
