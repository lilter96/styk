package main

import "testing"

func TestCatalogClientRequiresValidLocalSettings(t *testing.T) {
	for _, name := range []string{"OZON_HTTP_TIMEOUT", "OZON_REQUEST_INTERVAL", "OZON_RETRY_BACKOFF", "OZON_MAX_RETRY_AFTER", "OZON_MAX_ATTEMPTS"} {
		t.Setenv(name, "")
	}
	if catalogClient("", "synthetic-key") != nil || catalogClient("synthetic", "") != nil {
		t.Fatal("missing credentials accepted")
	}
	if catalogClient("synthetic", "synthetic-key") == nil {
		t.Fatal("default client unavailable")
	}
	t.Setenv("OZON_REQUEST_INTERVAL", "invalid")
	if catalogClient("synthetic", "synthetic-key") != nil {
		t.Fatal("invalid rate configuration accepted")
	}
}
