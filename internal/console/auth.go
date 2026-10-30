// Package console exposes authenticated merchant operations.
package console

import (
	"errors"
	"net"
	"net/http"
)

type auditContextKey struct{}
type sessioner interface {
	Authorized(http.HandlerFunc) http.HandlerFunc
	SameOrigin(*http.Request) bool
}
type auth struct {
	merchant *merchantAuth
	boundary sessioner
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func ValidateAddress(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		return errors.New("console requires a loopback listen address")
	}
	return nil
}
func (a *auth) sameOrigin(r *http.Request) bool {
	if a.merchant != nil {
		return a.merchant.sameOrigin(r)
	}
	return a.boundary != nil && a.boundary.SameOrigin(r)
}
func (a *auth) authorized(next http.HandlerFunc) http.HandlerFunc {
	if a.merchant != nil {
		return a.merchant.authorized(next)
	}
	if a.boundary != nil {
		return a.boundary.Authorized(next)
	}
	return func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusUnauthorized, "UNAUTHORIZED") }
}
