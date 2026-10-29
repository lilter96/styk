package main

import "testing"

func TestLocalDatabaseRejectsRemoteAndConnectionStringOverride(t *testing.T) {
	tests := []struct {
		name, address string
		want          bool
	}{
		{"ipv4", "postgres://operator@127.0.0.1/styk", true},
		{"ipv6", "postgres://operator@[::1]/styk", true},
		{"remote", "postgres://operator@192.0.2.1/styk", false},
		{"override", "postgres://operator@localhost/styk?host=192.0.2.1", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := localDatabase(tc.address); got != tc.want {
				t.Fatalf("local database = %v, want %v", got, tc.want)
			}
		})
	}
}
