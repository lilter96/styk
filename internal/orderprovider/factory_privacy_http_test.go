package orderprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

func TestFactoryAppliesCustomerConsentToActualOzonDecoder(t *testing.T) {
	t.Parallel()
	for _, consent := range []bool{false, true} {
		t.Run(map[bool]string{false: "without consent", true: "with consent"}[consent], func(t *testing.T) {
			t.Parallel()
			config := privacyTestConfig()
			config.PIIEnabled = consent
			if !consent {
				config.PIIRetentionSeconds = 0
			}
			server := privacyHTTPServer(t)
			defer server.Close()
			factory := privacyHTTPFactory(t, config, server.URL)
			binding, err := factory.ResolveOrder(t.Context(), config.Scope, "ozon", config.Scope.OzonAccount, 1)
			if err != nil {
				t.Fatal(err)
			}
			posting, err := binding.Client.ReadOrderPosting(t.Context(), config.Scope, "posting")
			if err != nil || (posting.PII != nil) != consent {
				t.Fatalf("consent not applied: requested=%v error=%v", consent, err)
			}
			raw, err := json.Marshal(posting)
			if err != nil || strings.Contains(string(raw), "PRIVATE_") {
				t.Fatal("personal data escaped normal JSON")
			}
		})
	}
}

func privacyHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body string
		switch r.URL.Path {
		case "/v3/posting/fbs/get":
			body = `{"result":{"posting_number":"posting","order_id":5,"status":"awaiting_packaging","scheme":"FBS","delivery_method":{"warehouse_id":123,"id":456},"products":[],"customer":{"name":"PRIVATE_CUSTOMER","address":{"city":"PRIVATE_CITY"}}}}`
		case "/v2/warehouse/list":
			body = `{"warehouses":[{"warehouse_id":123,"warehouse_type":"fbs","status":"created","is_rfbs":false,"is_express":false}],"has_next":false}`
		case "/v2/delivery-method/list":
			body = `{"delivery_methods":[],"has_next":false}`
		default:
			t.Error("unexpected provider request")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Error(err)
		}
	}))
}

func privacyHTTPFactory(t *testing.T, config orders.Config, endpoint string) Factory {
	t.Helper()
	gate, err := providercall.New(privacyAdmissionStore{}, providercall.Options{SyntheticInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return Factory{Configs: &privacyConfigReader{config: config}, Credentials: privacyCredential{scope: config.Scope}, Gate: gate, NewOzon: func(account, secret string) (*ozon.Client, error) {
		return ozon.New(ozon.Options{BaseURL: endpoint, ClientID: account, APIKey: secret, Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	}}
}

type privacyCredential struct{ scope orders.Scope }

func (r privacyCredential) ResolveWithRevision(_ context.Context, scope credentials.Scope) (string, int64, error) {
	if scope.Tenant != r.scope.Tenant || scope.Provider != "ozon" || scope.Account != r.scope.OzonAccount {
		return "", 0, orders.ErrNotFound
	}
	return "synthetic-secret", 1, nil
}

type privacyAdmissionStore struct{}

func (privacyAdmissionStore) TryAcquireProviderCall(_ context.Context, a providercall.Attempt) (providercall.Admission, error) {
	return providercall.Admission{Lease: providercall.Lease{Attempt: a}}, nil
}
func (privacyAdmissionStore) StartProviderCall(context.Context, providercall.Lease) (time.Duration, error) {
	return 0, nil
}
func (privacyAdmissionStore) FinishProviderCall(context.Context, providercall.Lease, providercall.Result) error {
	return nil
}
