package providercall

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transportStore struct {
	finishErr bool
	started   int
	results   []Result
}

func (*transportStore) TryAcquireProviderCall(_ context.Context, a Attempt) (Admission, error) {
	return Admission{Lease: Lease{Attempt: a}}, nil
}
func (s *transportStore) StartProviderCall(context.Context, Lease) (time.Duration, error) {
	s.started++
	return 0, nil
}
func (s *transportStore) FinishProviderCall(_ context.Context, _ Lease, r Result) error {
	s.results = append(s.results, r)
	if s.finishErr {
		return errors.New("synthetic bookkeeping interruption")
	}
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTransportDoesNotConvertAcknowledgementIntoRetry(t *testing.T) {
	store := &transportStore{finishErr: true}
	gate, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := Transport{Gate: gate, Scope: Scope{Tenant: "merchant", Provider: "ozon", Account: "account", CredentialRevision: 1}, Timeout: time.Second, Base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"updated":true}`))}, nil
	})}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api-seller.ozon.ru/v2/products/stocks", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil || response == nil || response.StatusCode != 200 || calls != 1 {
		t.Fatal("provider acknowledgement was lost or replayed")
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	for _, result := range store.results {
		if result.Status != 200 {
			t.Fatal("cleanup erased acknowledged response")
		}
	}
}
func TestTransportFinalGuardRunsAfterAdmissionAndNeverDispatchesDeniedIntent(t *testing.T) {
	store := &transportStore{}
	gate, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	transport := Transport{Gate: gate, Scope: Scope{Tenant: "merchant", Provider: "moysklad", Account: "account", CredentialRevision: 1}, Timeout: time.Second, Base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("denied request reached provider")
		return nil, nil
	})}
	ctx := WithGuard(context.Background(), func(context.Context) error {
		if store.started != 1 {
			t.Fatal("guard preceded waiting admission")
		}
		return errors.New("configuration revoked")
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.moysklad.ru/api/remap/1.2/entity/customerorder", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if response != nil || !IsNotDispatched(err) {
		t.Fatal("local refusal was classified as ambiguous external outcome")
	}
}

func TestMutationAccountingDistinguishesAttributeWritesFromReadPOSTs(t *testing.T) {
	for _, tc := range []struct {
		path  string
		write bool
	}{
		{"/api/remap/1.2/entity/customerorder/metadata/attributes", true},
		{"/v1/product/update/attributes", true},
		{"/v1/description-category/attribute", false},
		{"/v1/description-category/attribute/values", false},
		{"/v3/products/info/attributes", false},
		{"/v2/product/info/stocks-by-warehouse/fbs", false},
		{"/v2/products/stocks", true},
		{"/v1/product/import/info", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://provider.invalid"+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if isMutation(request) != tc.write {
				t.Fatal("provider attempt was assigned to the wrong read/write budget")
			}
		})
	}
}
