package ozon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/providercall"
)

type labelAdmissionStore struct {
	attempts []providercall.Attempt
	started  int
}

func (s *labelAdmissionStore) TryAcquireProviderCall(_ context.Context, a providercall.Attempt) (providercall.Admission, error) {
	s.attempts = append(s.attempts, a)
	return providercall.Admission{Lease: providercall.Lease{Attempt: a}}, nil
}
func (s *labelAdmissionStore) StartProviderCall(context.Context, providercall.Lease) (time.Duration, error) {
	s.started++
	return 0, nil
}
func (*labelAdmissionStore) FinishProviderCall(context.Context, providercall.Lease, providercall.Result) error {
	return nil
}

func TestLabelPDFUsesAccountAdmissionWithoutForwardingCredentials(t *testing.T) {
	t.Parallel()
	store := &labelAdmissionStore{}
	gate, err := providercall.New(store, providercall.Options{})
	if err != nil {
		t.Fatal(err)
	}
	pdf := "%PDF-1.7\nSYNTHETIC\n%%EOF"
	httpClient := &http.Client{Transport: fbsLabelTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":{"code":"completed","postings_count":1,"printed_postings_count":1},"file_url":"https://api-seller.ozon.ru/label.pdf"}`))}, nil
		}
		if request.Header.Get("Api-Key") != "" || request.Header.Get("Client-Id") != "" {
			t.Error("credentials forwarded to file URL")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/pdf"}}, ContentLength: int64(len(pdf)), Body: io.NopCloser(strings.NewReader(pdf))}, nil
	})}
	base, err := New(Options{BaseURL: BaseURL, ClientID: "id", APIKey: "synthetic-secret", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	scope := orderTestScope()
	account := providercall.Scope{Tenant: scope.Tenant, Provider: "ozon", Account: scope.OzonAccount, CredentialRevision: 1}
	if err = base.Coordinate(gate, account); err != nil {
		t.Fatal(err)
	}
	client, err := NewOrderClient(base, scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.DownloadOrderLabel(context.Background(), scope, "https://api-seller.ozon.ru/label.pdf"); err == nil || store.started != 0 {
		t.Fatal("unverified URL reached network")
	}
	if _, err = client.ReadOrderLabelTask(context.Background(), scope, "99"); err != nil {
		t.Fatal(err)
	}
	data, err := client.DownloadOrderLabel(context.Background(), scope, "https://api-seller.ozon.ru/label.pdf")
	if err != nil || string(data) != pdf || store.started != 2 || len(store.attempts) != 2 {
		t.Fatalf("account admission missing: %v", err)
	}
	operation := sha256.Sum256([]byte("orders/label/pdf"))
	if store.attempts[1].Scope != account || store.attempts[1].IsWrite || store.attempts[1].Operation != hex.EncodeToString(operation[:]) {
		t.Fatal("wrong budget scope or write classification")
	}
	if _, err = client.DownloadOrderLabel(context.Background(), scope, "https://api-seller.ozon.ru/label.pdf"); err == nil || store.started != 2 {
		t.Fatal("one-use file authorization replayed")
	}
	scope.Tenant = "other"
	if _, err = client.DownloadOrderLabel(context.Background(), scope, "https://api-seller.ozon.ru/label.pdf"); err == nil || store.started != 2 {
		t.Fatal("cross-account file fetch admitted")
	}
}
