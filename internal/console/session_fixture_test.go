//go:build !integration

// Synthetic test adapter; never used by normal API startup.
package console

import (
	"context"
	"github.com/lilter96/styk/internal/sessionfixture"
	"net/http"
)

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
