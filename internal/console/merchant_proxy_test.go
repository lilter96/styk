package console

import (
	"net/http/httptest"
	"testing"
)

func TestMerchantTrustedProxyUsesLastUntrustedHop(t *testing.T) {
	t.Parallel()
	proxies, err := trustedProxies("127.0.0.0/8,::1/128")
	if err != nil {
		t.Fatal(err)
	}
	m := merchantAuth{proxies: proxies}
	r := httptest.NewRequest("POST", "http://127.0.0.1/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Add("X-Forwarded-For", "198.51.100.7")
	r.Header.Add("X-Forwarded-For", "203.0.113.8, 127.0.0.1")
	got, err := m.clientAddress(r)
	if err != nil || got != "203.0.113.8" {
		t.Fatalf("forwarded client = %q, err %v", got, err)
	}
	m.proxies = nil
	got, err = m.clientAddress(r)
	if err != nil || got != "127.0.0.1" {
		t.Fatalf("untrusted forwarding was accepted: %q %v", got, err)
	}
	if _, err = trustedProxies("0.0.0.0/0"); err == nil {
		t.Fatal("unsafe proxy range accepted")
	}
}
