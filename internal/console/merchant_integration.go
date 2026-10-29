//go:build integration

package console

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/postgres"
)

// MerchantLoopbackTestHandler is compiled only in integration tests. Both
// provider endpoints must be literal loopback HTTP addresses.
func MerchantLoopbackTestHandler(db *postgres.Store, origin string, sender identity.Sender, moyURL, ozonURL string) (http.Handler, error) {
	if !literalLoopbackURL(moyURL) || !literalLoopbackURL(ozonURL) {
		return nil, errors.New("synthetic providers require literal loopback endpoints")
	}
	a, err := newMerchantAuth(db, origin, sender)
	if err != nil {
		return nil, err
	}
	c := newConnections(db)
	c.catalogSimulator = true
	c.moyFactory = func(secret string) (*moysklad.Client, error) {
		return moysklad.New(moysklad.Options{BaseURL: moyURL, Token: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	}
	c.ozonFactory = func(account, secret string) (*ozon.Client, error) {
		return ozon.New(ozon.Options{AllowSyntheticStockWrites: true, BaseURL: ozonURL, ClientID: account, APIKey: secret, Timeout: 3 * time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	}
	return handlerWithAuth(db, a, c)
}

func literalLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}
