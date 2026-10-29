package providercall

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode"
)

func normalize(r Request, o Options) (Attempt, error) {
	s := r.Scope
	if !validText(s.Tenant, 256) || (s.Provider != "ozon" && s.Provider != "moysklad") || s.CredentialRevision < 0 || !validText(r.Method, 160) || !validText(r.Operation, 96) {
		return Attempt{}, ErrInvalid
	}
	if s.PendingID != "" {
		if s.Account != "" || s.CredentialRevision != 0 || !validText(s.PendingID, 256) {
			return Attempt{}, ErrInvalid
		}
		s.Account = "pending:" + digest(s.Tenant+"\x00"+s.PendingID)
	} else if !validText(s.Account, 256) || strings.HasPrefix(s.Account, "pending:") {
		return Attempt{}, ErrInvalid
	}
	interval := time.Second
	if o.SyntheticInterval > 0 && isLoopback(r.Endpoint) {
		interval = o.SyntheticInterval
	}
	if r.Timeout <= 0 {
		r.Timeout = 30 * time.Second
	}
	if r.Timeout > 15*time.Minute {
		return Attempt{}, ErrInvalid
	}
	a := Attempt{Scope: s, Method: digest(r.Method), Operation: digest(r.Operation), IsWrite: r.IsWrite, Interval: interval, MaxInflight: o.MaxInflight, LeaseDuration: r.Timeout + 5*time.Second}
	if r.TargetKey != "" {
		if !validText(r.TargetKey, 1024) {
			return Attempt{}, ErrInvalid
		}
		a.TargetKey, a.TargetInterval = digest(r.TargetKey), 120*time.Second
	}
	// The pending identifier is already hashed into the account namespace.
	a.Scope.PendingID = ""
	return a, nil
}

func validText(value string, max int) bool {
	return len(value) > 0 && len(value) <= max && !strings.ContainsFunc(value, unicode.IsControl)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func isLoopback(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
