package ozon

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/providercall"
)

func TestLabelTaskURLRejectsUnsafeNetworkTargets(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"http://cdn.example.test/label.pdf", "https://cdn.example.test:443/label.pdf", "https://user@cdn.example.test/label.pdf",
		"https://cdn.example.test/label.pdf#fragment", "https://cdn.example.test:/label.pdf", "https://127.0.0.1/label.pdf", "https://169.254.169.254/latest",
		"https://[::1]/label.pdf", "https://192.0.2.1/label.pdf", "https://localhost/label.pdf", "https://cdn.example.test./label.pdf",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if err := validateLabelTaskURL(raw); !errors.Is(err, ErrFBSLabelDownload) {
				t.Fatal("unsafe task file URL accepted")
			}
		})
	}
	if err := validateLabelTaskURL("https://cdn.example.test/label.pdf?private=token"); err != nil {
		t.Fatal(err)
	}
}

func TestPublicLabelAddressRejectsReservedRangesAndMappedPrivateIPv4(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"0.0.0.1", "10.1.1.1", "100.64.0.1", "127.1.1.1", "169.254.169.254", "172.31.1.1", "192.0.0.1", "192.0.2.1",
		"192.88.99.1", "192.168.1.1", "198.18.1.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::ffff:127.0.0.1", "::ffff:10.1.1.1", "fc00::1", "fe80::1", "64:ff9b::a00:1", "2001::1", "2001:db8::1", "2002:a00:1::", "3fff::1", "ff02::1",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if publicLabelIP(netip.MustParseAddr(raw)) {
				t.Fatal("nonpublic IP accepted")
			}
		})
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		if !publicLabelIP(netip.MustParseAddr(raw)) {
			t.Fatal("public IP rejected")
		}
	}
}

func TestLabelDialRejectsMixedDNSAnswersBeforeAnyConnect(t *testing.T) {
	t.Parallel()
	lookups, connects := 0, 0
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		lookups++
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	dial := func(context.Context, string, string) (net.Conn, error) { connects++; return nil, nil }
	_, err := dialPublicLabel(context.Background(), lookup, dial, "tcp", "cdn.example.test:443")
	if !errors.Is(err, ErrFBSLabelDownload) || lookups != 1 || connects != 0 {
		t.Fatal("mixed public/private DNS answer reached network")
	}
}

func TestLabelDialPinsValidatedAddressWithoutSecondDNSLookup(t *testing.T) {
	t.Parallel()
	lookups, connects := 0, 0
	lookup := func(_ context.Context, network, host string) ([]netip.Addr, error) {
		lookups++
		if network != "ip" || host != "cdn.example.test" {
			t.Error("unexpected lookup")
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		connects++
		if network != "tcp" || address != "8.8.8.8:443" {
			t.Error("hostname was resolved again")
		}
		return nil, nil
	}
	if _, err := dialPublicLabel(context.Background(), lookup, dial, "tcp", "cdn.example.test:443"); err != nil || lookups != 1 || connects != 1 {
		t.Fatal("public DNS pin failed")
	}
}

func TestTaskLabelDownloaderRequiresCoordinatedStandardTransport(t *testing.T) {
	t.Parallel()
	for _, transport := range []http.RoundTripper{http.DefaultTransport, fbsLabelTransport(nil), providercall.Transport{Base: fbsLabelTransport(nil)}} {
		if _, err := newFBSLabelDownloaderForTask(&http.Client{Transport: transport}, "https://cdn.example.test/label.pdf"); !errors.Is(err, ErrFBSLabelDownload) {
			t.Fatal("uncoordinated or unpinned CDN transport admitted")
		}
	}
	base := &http.Transport{Proxy: http.ProxyFromEnvironment}
	bound := providercall.Transport{Base: base, Timeout: time.Second}
	d, err := newFBSLabelDownloaderForTask(&http.Client{Transport: bound}, "https://cdn.example.test/label.pdf?private=token")
	if err != nil || d.taskURL == "" || !d.permits(d.taskURL) || d.permits("https://cdn.example.test/other.pdf") {
		t.Fatal("exact task URL permit missing")
	}
	pinned := d.http.Transport.(providercall.Transport).Base.(*http.Transport)
	if pinned == base || pinned.Proxy != nil || !pinned.DisableKeepAlives || base.Proxy == nil || base.DisableKeepAlives {
		t.Fatal("unsafe clone or caller transport mutated")
	}
}

func TestLabelPublicTransportRejectsCustomTLSDialsAndUnsafeTLS(t *testing.T) {
	t.Parallel()
	dial := (&net.Dialer{}).DialContext
	for _, base := range []*http.Transport{
		{DialTLSContext: dial}, {TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		{TLSClientConfig: &tls.Config{ServerName: "other.example.test"}},
		{TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{{}}}},
	} {
		if _, err := labelPublicTransport(base, net.DefaultResolver.LookupNetIP, dial); !errors.Is(err, ErrFBSLabelDownload) {
			t.Fatal("unsafe TLS transport accepted")
		}
	}
}

func TestLabelDialFailureIsRedactedAndDoesNotFallBack(t *testing.T) {
	t.Parallel()
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return nil, errors.New("PRIVATE DNS failure")
	}
	connects := 0
	dial := func(context.Context, string, string) (net.Conn, error) { connects++; return nil, nil }
	_, err := dialPublicLabel(context.Background(), lookup, dial, "tcp", "cdn.example.test:443")
	if !errors.Is(err, ErrFBSLabelDownload) || strings.Contains(err.Error(), "PRIVATE") || connects != 0 {
		t.Fatal("DNS failure leaked details or reached network")
	}
}

func TestPinnedLabelTransportPreservesOriginalTLSHostname(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS.ServerName != "example.com" || r.Host != "example.com" {
			t.Error("TLS or HTTP hostname replaced by pinned IP")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	base := server.Client().Transport.(*http.Transport)
	pinned, err := labelPublicTransport(base, lookup, labelTLSDialer(t, serverURL.Host))
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.CloseIdleConnections()
	response, err := (&http.Client{Transport: pinned}).Get("https://example.com/label.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func labelTLSDialer(t *testing.T, syntheticTarget string) labelDial {
	t.Helper()
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:443" {
			t.Error("unvalidated address dialed")
		}
		return (&net.Dialer{}).DialContext(ctx, network, syntheticTarget)
	}
}
