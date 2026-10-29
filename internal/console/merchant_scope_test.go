package console

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/identity"
)

func TestMerchantBodyScopeRejectsAmbiguousTenant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body string
		want       string
	}{
		{"valid", `{"tenant":"mine","value":2}`, "mine"},
		{"duplicate", `{"tenant":"mine","tenant":"other"}`, ""},
		{"case alias", `{"tenant":"mine","Tenant":"other"}`, ""},
		{"missing", `{"value":2}`, ""},
		{"nested", `{"value":{"tenant":"mine"}}`, ""},
		{"trailing", `{"tenant":"mine"}{}`, ""},
		{"null", `{"tenant":null}`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			got, err := bodyWorkspace(r)
			if (err == nil) != (tc.want != "") || got != tc.want {
				t.Fatalf("scope = %q, err=%v", got, err)
			}
			if tc.want != "" {
				remaining, err := io.ReadAll(r.Body)
				if err != nil || string(remaining) != tc.body {
					t.Fatal("scope check changed the handler request")
				}
			}
		})
	}
}

func TestMerchantRolesAndCSRF(t *testing.T) {
	t.Parallel()
	m := merchantAuth{origin: "https://seller.example.test"}
	session := identity.Session{CSRF: "secret-csrf-value"}
	r := httptest.NewRequest(http.MethodPost, "https://seller.example.test/api/imports", nil)
	r.Header.Set("Origin", m.origin)
	if m.checkCSRF(r, session) {
		t.Fatal("mutation without CSRF accepted")
	}
	r.Header.Set("X-STYK-CSRF", session.CSRF)
	if !m.checkCSRF(r, session) || rolePermits("VIEWER", r) || !rolePermits("OPERATOR", r) {
		t.Fatal("incorrect CSRF or operator permissions")
	}
	r.URL.Path = "/api/integrations/ozon/credentials"
	if rolePermits("OPERATOR", r) || !rolePermits("ADMIN", r) {
		t.Fatal("credential changes not restricted to administrators")
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
