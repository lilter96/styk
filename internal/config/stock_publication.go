package config

import (
	"net"
	"net/url"
	"os"
)

// StockPublicationTestEndpoint is deliberately limited to explicit loopback
// simulation until the real vendor can provide a verifiable write fence and
// the external contract has been independently validated. This is not a
// production enable switch. Empty, malformed and non-loopback values fail closed.
func StockPublicationTestEndpoint() (string, bool) {
	if os.Getenv("STYK_STOCK_PUBLICATION_ENABLED") != "true" {
		return "", false
	}
	value := os.Getenv("STYK_STOCK_PUBLICATION_TEST_ENDPOINT")
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" {
		return "", false
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return "", false
	}
	return value, true
}
