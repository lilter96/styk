package config

import "testing"

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:secret@localhost/test")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKER_MAX_ATTEMPTS", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid attempts")
	}
	t.Setenv("WORKER_MAX_ATTEMPTS", "3")
	t.Setenv("QUERY_TIMEOUT", "bad")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid duration")
	}
}
func TestMissingDB(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected missing database error")
	}
}
