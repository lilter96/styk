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
