package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestMerchantStartupRequiresExplicitSecurityConfiguration(t *testing.T) {
	t.Setenv("STYK_AUTH_MODE", "")
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", "")
	t.Setenv("STYK_SMTP_ADDRESS", "")
	t.Setenv("STYK_CONSOLE_ORIGIN", "")
	if _, err := configuredHandler(nil, "127.0.0.1:8080"); err == nil || !strings.Contains(err.Error(), "credential encryption") {
		t.Fatalf("default merchant mode accepted missing encryption configuration: %v", err)
	}
	t.Setenv("STYK_CREDENTIAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if _, err := configuredHandler(nil, "127.0.0.1:8080"); err == nil || !strings.Contains(err.Error(), "SMTP") {
		t.Fatalf("merchant mode accepted missing verification delivery: %v", err)
	}
	if _, err := configuredHandler(nil, "0.0.0.0:8080"); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("authenticated API accepted remote bind: %v", err)
	}
}

func TestRemovedLocalModeIsRejected(t *testing.T) {
	t.Setenv("STYK_AUTH_MODE", "local")
	t.Setenv("STYK_ALLOW_REMOTE_BIND", "true")
	if _, err := configuredHandler(nil, "127.0.0.1:8080"); err == nil {
		t.Fatal("local operator mode accepted missing secret or database")
	}
	if _, err := configuredHandler(nil, "0.0.0.0:8080"); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("local operator mode accepted remote bind: %v", err)
	}
}
