package console

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lilter96/styk/internal/identity"
)

func TestPlatformAdminAccess(t *testing.T) {
	for _, path := range []string{"/api/admin/overview", "/api/admin/users"} {
		t.Run(path, func(t *testing.T) {
			mux := http.NewServeMux()
			registerPlatformAdmin(mux, &merchantAuth{origin: "http://127.0.0.1:5173", local: true}, nil)
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:5173"+path, nil)
			req.RemoteAddr = "127.0.0.1:12345"
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("anonymous response: %d %v", w.Code, w.Header())
			}
			called := false
			handler := platformAdminOnly("admin", func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})
			var session identity.Session
			session.User.ID = "merchant"
			w = httptest.NewRecorder()
			handler(w, req, session)
			if called || w.Code != http.StatusForbidden {
				t.Fatal("merchant could read platform data")
			}
			session.User.ID = "admin"
			w = httptest.NewRecorder()
			handler(w, req, session)
			if !called || w.Code != http.StatusOK {
				t.Fatal("platform admin could not read platform data")
			}
		})
	}
}

func TestConfiguredPlatformAdminRequiresOneID(t *testing.T) {
	t.Parallel()
	id := "u_0123456789abcdef0123456789abcdef"
	for _, raw := range []string{"", "admin@example.test", id + "," + id, id + " " + id} {
		if configuredPlatformAdmin(raw) != "" {
			t.Errorf("invalid administrator config accepted: %q", raw)
		}
	}
	if configuredPlatformAdmin(" "+id+" ") != id {
		t.Fatal("single administrator rejected")
	}
}

func TestSupportReadRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method, pattern string
		allowed         bool
	}{
		{http.MethodGet, "GET /api/session", true},
		{http.MethodGet, "GET /api/registrations", true},
		{http.MethodHead, "GET /api/products", true},
		{http.MethodPost, "POST /api/imports", false},
		{http.MethodDelete, "DELETE /api/session", false},
		{http.MethodGet, "GET /api/integrations/{provider}/credentials", false},
		{http.MethodGet, "GET /api/orders/artifacts/{workid}", false},
		{http.MethodGet, "GET /api/admin/users", false},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.pattern, func(t *testing.T) {
			if supportReadAllowed(&http.Request{Method: test.method, Pattern: test.pattern}) != test.allowed {
				t.Fatal("unexpected support access")
			}
		})
	}
}
