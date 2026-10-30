// Package sessionfixture provides synthetic sessions exclusively to Go test binaries.
package sessionfixture

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const CookieName = "styk_fixture"

type session struct {
	expires time.Time
	audit   string
	done    <-chan struct{}
	cancel  context.CancelFunc
}
type Auth struct {
	origin   string
	decorate func(context.Context, string) context.Context
	mu       sync.Mutex
	sessions map[[32]byte]session
}

func New(origin string, decorate func(context.Context, string) context.Context) (*Auth, error) {
	if !strings.HasSuffix(os.Args[0], ".test") && !strings.HasSuffix(os.Args[0], ".test.exe") {
		return nil, errors.New("synthetic sessions require a Go test binary")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !loopback(u.Hostname()) || decorate == nil {
		return nil, errors.New("fixture requires a loopback origin and audit decorator")
	}
	return &Auth{origin: strings.TrimSuffix(origin, "/"), decorate: decorate, sessions: make(map[[32]byte]session)}, nil
}
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func (a *Auth) safe(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && loopback(strings.Trim(host, "[]")) && loopback(peer) && (r.Header.Get("Origin") == "" || a.SameOrigin(r))
}
func (a *Auth) SameOrigin(r *http.Request) bool { return r.Header.Get("Origin") == a.origin }
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if !a.safe(r) || !a.SameOrigin(r) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, 403, "FORBIDDEN")
		return
	}
	token, err := a.issue()
	if err != nil {
		writeError(w, 503, "UNAVAILABLE")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 60 * 60})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
func (a *Auth) issue() (string, error) {
	var entropy [48]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for key, value := range a.sessions {
		if !value.expires.After(now) {
			value.cancel()
			delete(a.sessions, key)
		}
	}
	if len(a.sessions) >= 128 {
		return "", errors.New("fixture session capacity")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.sessions[sha256.Sum256(entropy[:32])] = session{expires: now.Add(8 * time.Hour), audit: base64.RawURLEncoding.EncodeToString(entropy[32:]), done: ctx.Done(), cancel: cancel}
	return base64.RawURLEncoding.EncodeToString(entropy[:32]), nil
}
func cookieKey(r *http.Request) ([32]byte, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return [32]byte{}, err
	}
	value, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || len(value) != 32 {
		return [32]byte{}, errors.New("invalid fixture cookie")
	}
	return sha256.Sum256(value), nil
}
func (a *Auth) read(r *http.Request) (session, bool) {
	key, err := cookieKey(r)
	if err != nil {
		return session{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	value, ok := a.sessions[key]
	if ok && !value.expires.After(time.Now()) {
		value.cancel()
		delete(a.sessions, key)
		ok = false
	}
	return value, ok
}
func (a *Auth) Authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !a.safe(r) {
			writeError(w, 403, "FORBIDDEN")
			return
		}
		value, ok := a.read(r)
		if !ok {
			writeError(w, 401, "UNAUTHORIZED")
			return
		}
		ctx, cancel := context.WithDeadline(r.Context(), value.expires)
		defer cancel()
		go func() {
			select {
			case <-value.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		select {
		case <-value.done:
			writeError(w, 401, "UNAUTHORIZED")
			return
		default:
		}
		next(w, r.WithContext(a.decorate(ctx, value.audit)))
	}
}
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if !a.SameOrigin(r) {
		writeError(w, 403, "FORBIDDEN")
		return
	}
	key, err := cookieKey(r)
	if err == nil {
		a.mu.Lock()
		if value, ok := a.sessions[key]; ok {
			value.cancel()
			delete(a.sessions, key)
		}
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}
func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": code}); err != nil {
		return
	}
}
