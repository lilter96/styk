//go:build integration

// Test fixture only. Normal builds contain no local sign-in endpoint.
package console

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"github.com/lilter96/styk/internal/postgres"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func newAuth(_, origin string) (*auth, error) {
	if !strings.HasSuffix(os.Args[0], ".test") && !strings.HasSuffix(os.Args[0], ".test.exe") {
		return nil, errors.New("session fixture requires a test binary")
	}
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !loopbackHost(u.Hostname()) {
		return nil, errors.New("console origin must be a loopback HTTP origin")
	}
	return &auth{origin: strings.TrimSuffix(origin, "/"), sessions: map[[32]byte]operatorSession{}}, nil
}
func (a *auth) login(w http.ResponseWriter, r *http.Request) {
	if !a.safe(r) || r.Header.Get("Origin") != a.origin || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	var token [32]byte
	var audit [16]byte
	if _, e := rand.Read(token[:]); e != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	if _, e := rand.Read(audit[:]); e != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	a.mu.Lock()
	now := time.Now()
	for k, v := range a.sessions {
		if !v.expires.After(now) {
			v.cancel()
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= 128 {
		a.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	a.sessions[sha256.Sum256(token[:])] = operatorSession{expires: now.Add(8 * time.Hour), auditID: base64.RawURLEncoding.EncodeToString(audit[:]), ctx: sessionCtx, cancel: sessionCancel}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: base64.RawURLEncoding.EncodeToString(token[:]), Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 60 * 60})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// Handler exposes scoped reads and explicit local mapping operations to an authenticated local operator.
func Handler(db *postgres.Store, secret, origin string) (http.Handler, error) {
	return handlerWithConnections(db, secret, origin, nil)
}

// Injection is used by package tests; the production handler always constructs
// clients for the official provider hosts from local process configuration.
func handlerWithConnections(db *postgres.Store, secret, origin string, connections *connections) (http.Handler, error) {
	if db == nil {
		return nil, errors.New("console database required")
	}
	a, e := newAuth(secret, origin)
	if e != nil {
		return nil, e
	}
	return fixtureHandler(db, a, connections)
}

func fixtureHandler(db *postgres.Store, a *auth, c *connections) (http.Handler, error) {
	h, err := handlerWithAuth(db, a, c)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/session", a.login)
	mux.HandleFunc("GET /api/session", a.authorized(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	mux.HandleFunc("DELETE /api/session", a.authorized(a.logout))
	mux.Handle("/", h)
	return mux, nil
}
