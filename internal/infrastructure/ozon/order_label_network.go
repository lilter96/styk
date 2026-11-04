package ozon

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/providercall"
)

// newFBSLabelDownloaderForTask must only receive the exact file URL from an
// authenticated label-task response. The caller owns that provenance check.
func newFBSLabelDownloaderForTask(client *http.Client, rawURL string) (*FBSLabelDownloader, error) {
	if validateLabelTaskURL(rawURL) != nil {
		return nil, ErrFBSLabelDownload
	}
	downloader, err := NewFBSLabelDownloader(client)
	if err != nil || ValidateFBSLabelURL(rawURL) == nil {
		return downloader, err
	}
	bound, ok := downloader.http.Transport.(providercall.Transport)
	if !ok {
		return nil, ErrFBSLabelDownload
	}
	base, ok := bound.Base.(*http.Transport)
	if !ok {
		return nil, ErrFBSLabelDownload
	}
	transport, err := labelPublicTransport(base, net.DefaultResolver.LookupNetIP, (&net.Dialer{Timeout: 10 * time.Second}).DialContext)
	if err != nil {
		return nil, err
	}
	bound.Base = transport
	downloader.http.Transport, downloader.taskURL = bound, rawURL
	return downloader, nil
}

func validateLabelTaskURL(raw string) error {
	if len(raw) == 0 || len(raw) > 8192 || strings.ContainsAny(raw, "\r\n\x00") {
		return ErrFBSLabelDownload
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Fragment != "" || u.Port() != "" || u.Path == "" {
		return ErrFBSLabelDownload
	}
	host := u.Hostname()
	if u.Host != host || len(host) > 253 || !strings.Contains(host, ".") || strings.HasSuffix(host, ".") || strings.ContainsAny(host, "[]:%\\ ") {
		return ErrFBSLabelDownload
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil && !publicLabelIP(ip) {
		return ErrFBSLabelDownload
	}
	return nil
}

type labelLookup func(context.Context, string, string) ([]netip.Addr, error)
type labelDial func(context.Context, string, string) (net.Conn, error)

func labelPublicTransport(base *http.Transport, lookup labelLookup, dial labelDial) (*http.Transport, error) {
	if base == nil || lookup == nil || dial == nil || base.DialTLSContext != nil {
		return nil, ErrFBSLabelDownload
	}
	if tls := base.TLSClientConfig; tls != nil && (tls.InsecureSkipVerify || tls.ServerName != "" || tls.VerifyPeerCertificate != nil || tls.VerifyConnection != nil || len(tls.Certificates) != 0 || tls.GetClientCertificate != nil) {
		return nil, ErrFBSLabelDownload
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if base.TLSClientConfig != nil {
		config = base.TLSClientConfig.Clone()
		config.MinVersion = max(config.MinVersion, tls.VersionTLS12)
	}
	config.NextProtos = []string{"http/1.1"}
	// Copy only reviewed TLS options. In particular, inheriting a legacy custom
	// TLS dial hook could bypass DialContext and its public-address validation.
	transport := &http.Transport{TLSClientConfig: config, DisableKeepAlives: true,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		MaxResponseHeaderBytes: 64 << 10, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialPublicLabel(ctx, lookup, dial, network, address)
	}
	return transport, nil
}

func dialPublicLabel(ctx context.Context, lookup labelLookup, dial labelDial, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" {
		return nil, ErrFBSLabelDownload
	}
	ips, err := lookup(ctx, "ip", host)
	if err != nil || len(ips) == 0 || len(ips) > 64 {
		return nil, ErrFBSLabelDownload
	}
	for _, ip := range ips {
		if !publicLabelIP(ip) {
			return nil, ErrFBSLabelDownload
		}
	}
	// Dial the validated numerical address, never the hostname a second time.
	// The HTTP transport retains the original hostname for TLS verification.
	return dial(ctx, network, net.JoinHostPort(ips[0].Unmap().String(), port))
}

var deniedLabelNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

var publicLabelIPv6 = netip.MustParsePrefix("2000::/3")

func publicLabelIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !publicLabelIPv6.Contains(ip) {
		return false
	}
	for _, prefix := range deniedLabelNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
