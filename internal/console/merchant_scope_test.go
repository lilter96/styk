package console

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/identity"
)

func TestMerchantBodyScopeUsesAuthenticatedAccount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body string
		allowed    bool
	}{
		{"valid", `{"tenant":"mine","value":2}`, true},
		{"foreign", `{"tenant":"other"}`, false},
		{"duplicate", `{"tenant":"mine","tenant":"other"}`, false},
		{"case alias", `{"tenant":"mine","Tenant":"other"}`, false},
		{"missing", `{"value":2}`, true},
		{"nested", `{"value":{"tenant":"other"}}`, true},
		{"trailing", `{"tenant":"mine"}{}`, false},
		{"null", `{"tenant":null}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			err := scopeMerchantBody(r, "mine")
			if (err == nil) != tc.allowed {
				t.Fatalf("scope error = %v, allowed=%v", err, tc.allowed)
			}
			if tc.allowed {
				remaining, err := io.ReadAll(r.Body)
				var decoded map[string]any
				if err != nil || json.Unmarshal(remaining, &decoded) != nil || decoded["tenant"] != "mine" {
					t.Fatal("handler did not receive the authenticated account")
				}
			}
		})
	}
}

func TestMerchantCSRF(t *testing.T) {
	t.Parallel()
	m := merchantAuth{origin: "https://seller.example.test"}
	session := identity.Session{CSRF: "secret-csrf-value"}
	r := httptest.NewRequest(http.MethodPost, "https://seller.example.test/api/imports", nil)
	r.Header.Set("Origin", m.origin)
	if m.checkCSRF(r, session) {
		t.Fatal("mutation without CSRF accepted")
	}
	r.Header.Set("X-STYK-CSRF", session.CSRF)
	if !m.checkCSRF(r, session) {
		t.Fatal("same-origin request with CSRF token rejected")
	}
	r.Header.Set("Origin", "https://attacker.example.test")
	if m.checkCSRF(r, session) {
		t.Fatal("cross-origin mutation accepted")
	}
}

func TestMerchantLocalOriginRequiresMatchingLoopbackHostAndPort(t *testing.T) {
	t.Parallel()
	m := merchantAuth{origin: "http://127.0.0.1:5173", local: true}
	tests := []struct {
		name, host, origin string
		allowed            bool
	}{
		{"canonical", "127.0.0.1:5173", "http://127.0.0.1:5173", true},
		{"localhost", "localhost:5173", "http://localhost:5173", true},
		{"different loopback host", "localhost:5173", "http://127.0.0.1:5173", false},
		{"different port", "localhost:5174", "http://localhost:5174", false},
		{"foreign host", "attacker.example:5173", "http://attacker.example:5173", false},
		{"foreign origin", "localhost:5173", "http://attacker.example:5173", false},
		{"userinfo", "localhost:5173", "http://attacker@localhost:5173", false},
		{"query", "localhost:5173", "http://localhost:5173?", false},
		{"null origin", "localhost:5173", "null", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/api/session", nil)
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-STYK-CSRF", "csrf-value")
			if got := m.sameOrigin(r); got != tc.allowed {
				t.Fatalf("sameOrigin = %v, want %v", got, tc.allowed)
			}
			if got := m.safe(r) && m.checkCSRF(r, identity.Session{CSRF: "csrf-value"}); got != tc.allowed {
				t.Fatalf("authorized origin = %v, want %v", got, tc.allowed)
			}
		})
	}
}
