// Package console implements the local operator's read-only interface.
package console

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const cookieName = "styk_operator"

type operatorSession struct {
	expires time.Time
	auditID string
	ctx     context.Context
	cancel  context.CancelFunc
}
type auditContextKey struct{}

type auth struct {
	origin   string
	merchant *merchantAuth
	mu       sync.Mutex
	sessions map[[32]byte]operatorSession
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateAddress prevents accidental exposure of the local operator interface.
func ValidateAddress(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		return errors.New("console requires a loopback listen address")
	}
	return nil
}

func (a *auth) safe(r *http.Request) bool {
	host, _, e := net.SplitHostPort(r.Host)
	if e != nil {
		host = r.Host
	}
	if !loopbackHost(strings.Trim(host, "[]")) {
		return false
	}
	remote, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil || !loopbackHost(remote) {
		return false
	}
	origin := r.Header.Get("Origin")
	return origin == "" || origin == a.origin
}

func (a *auth) sameOrigin(r *http.Request) bool {
	if a.merchant != nil {
		return a.merchant.sameOrigin(r)
	}
	return r.Header.Get("Origin") == a.origin
}

func (a *auth) authorized(next http.HandlerFunc) http.HandlerFunc {
	if a.merchant != nil {
		return a.merchant.authorized(next)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !a.safe(r) {
			writeError(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		cookie, e := r.Cookie(cookieName)
		if e != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
			return
		}
		token, e := base64.RawURLEncoding.DecodeString(cookie.Value)
		if e != nil || len(token) != 32 {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
			return
		}
		key := sha256.Sum256(token)
		a.mu.Lock()
		session, ok := a.sessions[key]
		if ok && !session.expires.After(time.Now()) {
			session.cancel()
			delete(a.sessions, key)
			ok = false
		}
		a.mu.Unlock()
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
			return
		}
		// Cancellation also reaches in-flight database operations on logout;
		// a request cannot keep working beyond the authenticated session expiry.
		ctx, cancel := context.WithDeadline(r.Context(), session.expires)
		stop := context.AfterFunc(session.ctx, cancel)
		defer cancel()
		defer stop()
		if session.ctx.Err() != nil || ctx.Err() != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
			return
		}
		next(w, r.WithContext(context.WithValue(ctx, auditContextKey{}, session.auditID)))
	}
}

func (a *auth) logout(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != a.origin {
		writeError(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	if c, e := r.Cookie(cookieName); e == nil {
		if v, e := base64.RawURLEncoding.DecodeString(c.Value); e == nil {
			a.mu.Lock()
			key := sha256.Sum256(v)
			if session, ok := a.sessions[key]; ok {
				session.cancel()
				delete(a.sessions, key)
			}
			a.mu.Unlock()
		}
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}
