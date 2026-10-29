package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/postgres"
)

func TestStockSyncRoutesRequireSessionOriginAndExplicitAcknowledgement(t *testing.T) {
	origin := "http://127.0.0.1:15177"
	a, err := newAuth(strings.Repeat("s", 32), origin)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/session", a.login)
	db := &postgres.Store{}
	registerStockSyncRoutes(mux, a, db, &connections{db: db})
	send := func(path, body string, cookie *http.Cookie, requestOrigin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:15177"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:31337"
		r.Header.Set("Origin", requestOrigin)
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/stock-sync/preview", "/api/stock-sync/enable", "/api/stock-sync/disable", "/api/stock-sync/operations/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/reconcile"} {
		if got := send(path, `{}`, nil, origin).Code; got != 401 {
			t.Fatalf("unauthenticated %s: %d", path, got)
		}
	}
	login := send("/api/session", `{"secret":"`+strings.Repeat("s", 32)+`"}`, nil, origin)
	if login.Code != 204 {
		t.Fatalf("login: %d", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	body := `{"tenant":"tenant","moysklad_account":"moy","ozon_account":"ozon","idempotency_key":"test-intent-key-1234","acknowledge":false}`
	for _, path := range []string{"/api/stock-sync/enable", "/api/stock-sync/disable"} {
		if got := send(path, body, cookie, origin).Code; got != 400 {
			t.Fatalf("unconfirmed %s: %d", path, got)
		}
		if got := send(path, body, cookie, "http://localhost:evil").Code; got != 403 {
			t.Fatalf("cross-origin %s: %d", path, got)
		}
	}
}

func TestStockPublicationDeploymentGateCannotBeBypassedByEnableRequest(t *testing.T) {
	origin := "http://127.0.0.1:15177"
	auth, err := newAuth(strings.Repeat("s", 32), origin)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/session", auth.login)
	db := &postgres.Store{}
	registerStockSyncRoutes(mux, auth, db, &connections{db: db})
	send := func(path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", origin+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:31337"
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	login := send("/api/session", `{"secret":"`+strings.Repeat("s", 32)+`"}`, nil)
	cookie := login.Result().Cookies()[0]
	body := `{"tenant":"tenant","moysklad_account":"moy","ozon_account":"ozon","review_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","idempotency_key":"synthetic-key-12345678","acknowledge":true}`
	for _, tc := range []struct{ flag, endpoint string }{{"", ""}, {"true", ""}, {"true", "https://api-seller.ozon.ru"}, {"true", "http://localhost:1234"}} {
		t.Setenv("STYK_STOCK_PUBLICATION_ENABLED", tc.flag)
		t.Setenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT", tc.endpoint)
		w := send("/api/stock-sync/enable", body, cookie)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "STOCK_PUBLICATION_DISABLED") {
			t.Fatalf("gate bypass: %d %s", w.Code, w.Body.String())
		}
	}
}
