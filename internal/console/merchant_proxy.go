package console

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func trustedProxies(raw string) ([]netip.Prefix, error) {
	if raw == "" {
		return nil, nil
	}
	items := strings.Split(raw, ",")
	if len(items) > 16 {
		return nil, errors.New("too many trusted proxy ranges")
	}
	prefixes := make([]netip.Prefix, 0, len(items))
	for _, item := range items {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil || !prefix.Masked().Addr().IsLoopback() {
			return nil, errors.New("trusted proxy ranges must be loopback CIDRs")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func (m *merchantAuth) trustedProxy(addr netip.Addr) bool {
	for _, prefix := range m.proxies {
		if prefix.Contains(addr.Unmap()) {
			return true
		}
	}
	return false
}

// Only configured loopback proxies may supply X-Forwarded-For. Walking from
// the right finds the first non-proxy hop after a correctly appending proxy.
func (m *merchantAuth) clientAddress(r *http.Request) (string, error) {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "", err
	}
	address, err := netip.ParseAddr(peer)
	if err != nil {
		return "", err
	}
	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if !m.trustedProxy(address) || forwarded == "" {
		return address.Unmap().String(), nil
	}
	chain := strings.Split(forwarded, ",")
	if len(chain) > 16 {
		return "", errors.New("forwarded chain too long")
	}
	for i := len(chain) - 1; i >= 0; i-- {
		address, err = netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			return "", errors.New("invalid forwarded address")
		}
		if !m.trustedProxy(address) {
			return address.Unmap().String(), nil
		}
	}
	return address.Unmap().String(), nil
}
