// Package console implements the local operator's read-only interface.
package console

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const cookieName = "styk_operator"

type auth struct {
	secret   [32]byte
	origin   string
	mu       sync.Mutex
	sessions map[[32]byte]time.Time
}

func newAuth(secret, origin string) (*auth, error) {
	if len(secret) < 32 {
		return nil, errors.New("STYK_OPERATOR_SECRET must contain at least 32 characters")
	}
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !loopbackHost(u.Hostname()) {
		return nil, errors.New("console origin must be a loopback HTTP origin")
	}
	return &auth{secret: sha256.Sum256([]byte(secret)), origin: strings.TrimSuffix(origin, "/"), sessions: map[[32]byte]time.Time{}}, nil
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

func (a *auth) login(w http.ResponseWriter, r *http.Request) {
	if !a.safe(r) || r.Header.Get("Origin") != a.origin || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	var body struct {
		Secret string `json:"secret"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	sum := sha256.Sum256([]byte(body.Secret))
	if subtle.ConstantTimeCompare(sum[:], a.secret[:]) != 1 {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	var token [32]byte
	if _, e := rand.Read(token[:]); e != nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	a.mu.Lock()
	now := time.Now()
	for k, v := range a.sessions {
		if !v.After(now) {
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= 128 {
		a.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	a.sessions[sha256.Sum256(token[:])] = now.Add(8 * time.Hour)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: base64.RawURLEncoding.EncodeToString(token[:]), Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 60 * 60})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
func (a *auth) authorized(next http.HandlerFunc) http.HandlerFunc {
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
		expiry, ok := a.sessions[key]
		if ok && !expiry.After(time.Now()) {
			delete(a.sessions, key)
			ok = false
		}
		a.mu.Unlock()
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
			return
		}
		next(w, r)
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
			delete(a.sessions, sha256.Sum256(v))
			a.mu.Unlock()
		}
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}
