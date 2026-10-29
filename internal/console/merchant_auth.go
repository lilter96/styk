package console

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/postgres"
)

const merchantCookie = "styk_session"

type merchantSessionKey struct{}

type merchantAuth struct {
	identity *identity.Store
	db       *postgres.Store
	sender   identity.Sender
	origin   string
	host     string
	local    bool
	secure   bool
	proxies  []netip.Prefix
}

func newMerchantAuth(db *postgres.Store, origin string, sender identity.Sender) (*auth, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("merchant origin must be an exact HTTPS origin")
	}
	local := loopbackHost(u.Hostname())
	if u.Scheme != "https" && (u.Scheme != "http" || !local) {
		return nil, errors.New("merchant origin must be HTTPS except on loopback")
	}
	if sender == nil {
		return nil, errors.New("merchant verification and recovery delivery must be configured")
	}
	proxies, err := trustedProxies(os.Getenv("STYK_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return nil, err
	}
	store, err := identity.New(db.Pool, db.Timeout)
	if err != nil {
		return nil, err
	}
	normalized := strings.TrimSuffix(origin, "/")
	merchant := &merchantAuth{identity: store, db: db, sender: sender, origin: normalized, host: u.Host, local: local, secure: u.Scheme == "https", proxies: proxies}
	return &auth{origin: normalized, merchant: merchant}, nil
}

func (m *merchantAuth) safe(r *http.Request) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !loopbackHost(peer) {
		return false
	}
	if m.local {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if !loopbackHost(strings.Trim(host, "[]")) {
			return false
		}
	} else if !strings.EqualFold(r.Host, m.host) {
		return false
	}
	origin := r.Header.Get("Origin")
	return origin == "" || origin == m.origin
}

func (m *merchantAuth) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !m.safe(r) {
			writeError(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		session, err := m.readSession(r)
		if errors.Is(err, identity.ErrAuth) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE")
			return
		}
		if !m.checkCSRF(r, session) {
			writeError(w, http.StatusForbidden, "FORBIDDEN")
			return
		}
		ctx, cancel := context.WithDeadline(r.Context(), session.Expires)
		defer cancel()
		ctx = context.WithValue(ctx, merchantSessionKey{}, session)
		ctx = context.WithValue(ctx, auditContextKey{}, session.AuditID)
		ctx = identity.WithSession(ctx, session)
		r = r.WithContext(ctx)
		if !m.authorizeScope(w, r, session) {
			return
		}
		next(w, r)
	}
}

func (m *merchantAuth) readSession(r *http.Request) (identity.Session, error) {
	cookie, err := r.Cookie(merchantCookie)
	if err != nil {
		return identity.Session{}, identity.ErrAuth
	}
	return m.identity.Session(r.Context(), cookie.Value)
}

func (m *merchantAuth) checkCSRF(r *http.Request, session identity.Session) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	return r.Header.Get("Origin") == m.origin && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-STYK-CSRF")), []byte(session.CSRF)) == 1
}

func sessionFrom(r *http.Request) identity.Session {
	session, _ := r.Context().Value(merchantSessionKey{}).(identity.Session)
	return session
}

func actorProvenance(r *http.Request) string {
	audit, _ := r.Context().Value(auditContextKey{}).(string)
	if sessionFrom(r).AuditID != "" {
		return "MERCHANT_SESSION:" + audit
	}
	return "LOCAL_OPERATOR_SESSION:" + audit
}
