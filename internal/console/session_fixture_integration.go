//go:build integration

// Synthetic test adapter; never used by normal API startup.
package console

import (
	"context"
	"errors"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/sessionfixture"
	"net/http"
)

const cookieName = sessionfixture.CookieName

func newAuth(_, origin string) (*auth, error) {
	fixture, err := sessionfixture.New(origin, func(ctx context.Context, id string) context.Context {
		return context.WithValue(ctx, auditContextKey{}, id)
	})
	if err != nil {
		return nil, err
	}
	return &auth{boundary: fixture}, nil
}
func (a *auth) login(w http.ResponseWriter, r *http.Request) {
	a.boundary.(*sessionfixture.Auth).Login(w, r)
}
func (a *auth) logout(w http.ResponseWriter, r *http.Request) {
	a.boundary.(*sessionfixture.Auth).Logout(w, r)
}
func Handler(db *postgres.Store, ignored, origin string) (http.Handler, error) {
	return handlerWithConnections(db, ignored, origin, nil)
}
func handlerWithConnections(db *postgres.Store, ignored, origin string, c *connections) (http.Handler, error) {
	if db == nil {
		return nil, errors.New("fixture database required")
	}
	a, err := newAuth(ignored, origin)
	if err != nil {
		return nil, err
	}
	h, err := handlerWithAuth(db, a, c)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/session", a.login)
	mux.HandleFunc("DELETE /api/session", a.authorized(a.logout))
	mux.HandleFunc("GET /api/session", a.authorized(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	mux.Handle("/", h)
	return mux, nil
}
