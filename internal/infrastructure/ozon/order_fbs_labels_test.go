package ozon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type fbsLabelTransport func(*http.Request) (*http.Response, error)

func (transport fbsLabelTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestFBSLabelURLRejectsArbitraryOrigins(t *testing.T) {
	t.Parallel()
	tests := []string{
		"http://api-seller.ozon.ru/label.pdf", "https://127.0.0.1/label.pdf", "https://localhost/label.pdf", "https://169.254.169.254/latest",
		"https://api-seller.ozon.ru.example.com/label.pdf", "https://api-seller.ozon.ru:8443/label.pdf", "https://api-seller.ozon.ru./label.pdf",
		"https://user@api-seller.ozon.ru/label.pdf", "https://api-seller.ozon.ru/label.pdf#fragment", "//api-seller.ozon.ru/label.pdf", "https://unverified-cdn.ozone.ru/label.pdf",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if err := ValidateFBSLabelURL(raw); !errors.Is(err, ErrFBSLabelDownload) {
				t.Fatalf("untrusted label URL accepted: %v", err)
			}
		})
	}
	if err := ValidateFBSLabelURL("https://api-seller.ozon.ru/fixture.pdf?token=fixture"); err != nil {
		t.Fatal(err)
	}
}

func TestFBSLabelDownloadBoundsAndContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mime    string
		body    string
		length  int64
		wantErr bool
	}{
		{name: "pdf", mime: "application/pdf", body: "%PDF-1.7\nfixture\n%%EOF", length: -1},
		{name: "html mime", mime: "text/html", body: "%PDF-1.7\nfixture\n%%EOF", length: -1, wantErr: true},
		{name: "html body", mime: "application/pdf", body: "<html>not PDF</html>", length: -1, wantErr: true},
		{name: "truncated", mime: "application/pdf", body: "%PDF-1.7\nfixture", length: -1, wantErr: true},
		{name: "length mismatch", mime: "application/pdf", body: "%PDF-1.7\nfixture\n%%EOF", length: 1000, wantErr: true},
		{name: "declared oversized", mime: "application/pdf", length: maxFBSLabelBytes + 1, wantErr: true},
		{name: "chunked oversized", mime: "application/pdf", body: "%PDF-1.7\n" + strings.Repeat("x", maxFBSLabelBytes) + "\n%%EOF", length: -1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := fbsLabelClient(t, &calls, tc.mime, tc.body, tc.length)
			data, err := client.Download(context.Background(), "https://api-seller.ozon.ru/fixture.pdf?token=fixture")
			if (err != nil) != tc.wantErr || calls.Load() != 1 || tc.wantErr && len(data) != 0 {
				t.Fatalf("unsafe or repeated download: len=%d calls=%d err=%v", len(data), calls.Load(), err)
			}
		})
	}
}

func fbsLabelClient(t *testing.T, calls *atomic.Int32, contentType, body string, length int64) *FBSLabelDownloader {
	t.Helper()
	client, err := NewFBSLabelDownloader(&http.Client{Transport: fbsLabelTransport(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.Method != http.MethodGet || request.Header.Get("Api-Key") != "" || request.Header.Get("Client-Id") != "" {
			t.Error("download must not carry seller credentials")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, ContentLength: length, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestFBSLabelDownloadRejectsRedirectsWithoutFollowing(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	httpClient := &http.Client{Transport: fbsLabelTransport(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"http://169.254.169.254/latest"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})}
	client, err := NewFBSLabelDownloader(httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Download(context.Background(), "https://api-seller.ozon.ru/fixture.pdf"); !errors.Is(err, ErrFBSLabelDownload) || calls.Load() != 1 {
		t.Fatalf("redirect followed: calls=%d err=%v", calls.Load(), err)
	}
	if httpClient.CheckRedirect != nil {
		t.Fatal("constructor mutated caller's HTTP client")
	}
}

func TestFBSLabelDownloadErrorsDoNotExposeSignedURL(t *testing.T) {
	t.Parallel()
	client, err := NewFBSLabelDownloader(&http.Client{Transport: fbsLabelTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport fixture failure")
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Download(context.Background(), "https://api-seller.ozon.ru/fixture.pdf?token=private-fixture")
	if !errors.Is(err, ErrFBSLabelDownload) || strings.Contains(err.Error(), "private-fixture") {
		t.Fatalf("signed URL exposed or failure hidden: %v", err)
	}
}

func TestFBSLabelDownloadRejectsURLBeforeTransport(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	client := fbsLabelClient(t, &calls, "application/pdf", "%PDF-1.7\nfixture\n%%EOF", -1)
	_, err := client.Download(context.Background(), "http://169.254.169.254/latest")
	if !errors.Is(err, ErrFBSLabelDownload) || calls.Load() != 0 {
		t.Fatalf("untrusted URL reached transport: calls=%d err=%v", calls.Load(), err)
	}
}
