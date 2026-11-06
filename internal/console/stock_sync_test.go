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
	a, err := newFixtureAuth(strings.Repeat("s", 32), origin)
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
	for _, path := range []string{"/api/stock-sync/preview", "/api/stock-sync/enable", "/api/stock-sync/disable", "/api/stock-sync/operations/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/reconcile", "/api/stock-sync/operations/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/supersede"} {
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
	t.Setenv("STYK_TEST_ACCOUNT_PUBLICATION", "true")
	wrongScope := `{"tenant":"","moysklad_account":"moy","ozon_account":"ozon","idempotency_key":"synthetic-recovery-key","expected_readback_at":"2026-10-29T13:27:08Z","expected_quantity":"0","reason":"synthetic operator accepted uncertainty","acknowledge":true,"worker_stopped":true}`
	if got := send("/api/stock-sync/operations/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/supersede", wrongScope, cookie, origin).Code; got != 400 {
		t.Fatalf("invalid recovery scope: %d", got)
	}
}
