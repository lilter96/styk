package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalSessionProtectsBusinessData(t *testing.T) {
	secret := strings.Repeat("s", 32)
	a, e := newAuth(secret, "http://127.0.0.1:5173")
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /session", a.login)
	mux.HandleFunc("GET /data", a.authorized(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := func(method, path, body, origin, host, remote string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
		r.Host = host
		r.RemoteAddr = remote
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if method == "POST" {
			r.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/data", "", "", "127.0.0.1:8080", "127.0.0.1:1234", nil); w.Code != 401 {
		t.Fatal("unauthenticated request accepted")
	}
	if w := request("POST", "/session", `{"secret":"`+secret+`"}`, "http://attacker.invalid", "127.0.0.1:8080", "127.0.0.1:1234", nil); w.Code != 403 {
		t.Fatal("cross-origin login accepted")
	}
	w := request("POST", "/session", `{"secret":"`+secret+`"}`, "http://127.0.0.1:5173", "127.0.0.1:8080", "127.0.0.1:1234", nil)
	if w.Code != 204 || len(w.Result().Cookies()) != 1 {
		t.Fatal("login failed")
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	if w = request("GET", "/data", "", "", "127.0.0.1:8080", "127.0.0.1:1234", c); w.Code != 204 {
		t.Fatal("authorized request rejected")
	}
	if w = request("GET", "/data", "", "", "evil.invalid:8080", "127.0.0.1:1234", c); w.Code != 403 {
		t.Fatal("untrusted host accepted")
	}
	if w = request("GET", "/data", "", "", "127.0.0.1:8080", "192.0.2.1:1234", c); w.Code != 403 {
		t.Fatal("remote client accepted")
	}
}

func TestConsoleRequiresLoopbackAndStrongSecret(t *testing.T) {
	if _, e := newAuth("short", "http://127.0.0.1:5173"); e == nil {
		t.Fatal("weak secret accepted")
	}
	if _, e := newAuth(strings.Repeat("x", 32), "http://evil.invalid"); e == nil {
		t.Fatal("remote origin accepted")
	}
	if ValidateAddress(":8080") == nil || ValidateAddress("0.0.0.0:8080") == nil || ValidateAddress("127.0.0.1:8080") != nil {
		t.Fatal("unsafe bind check")
	}
}
