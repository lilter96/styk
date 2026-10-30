package console

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnonymousAndRetiredCookieRemainUnauthorized(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, cookie string }{
		{name: "anonymous"},
		{name: "retired operator cookie", cookie: "c3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3Nzc3M"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := &auth{}
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/integrations", nil)
			req.RemoteAddr = "127.0.0.1:1234"
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: cookieName, Value: tc.cookie})
			}
			response := httptest.NewRecorder()
			a.authorized(func(http.ResponseWriter, *http.Request) { t.Fatal("anonymous handler reached") })(response, req)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status: %d", response.Code)
			}
		})
	}
}

func TestConsoleRequiresLoopback(t *testing.T) {
	t.Parallel()
	if ValidateAddress(":8080") == nil || ValidateAddress("0.0.0.0:8080") == nil || ValidateAddress("127.0.0.1:8080") != nil {
		t.Fatal("unsafe bind check")
	}
}
