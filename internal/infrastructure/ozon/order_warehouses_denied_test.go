package ozon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/providercall"
)

func TestDeliveryMethodDenialMatchesOnlyExplicitDenial(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"provider_403", fmt.Errorf("wrapped: %w", &APIError{Status: 403}), true},
		{"admission_method_denied", fmt.Errorf("wrapped: %w", providercall.ErrMethodForbidden), true},
		{"unauthorized", &APIError{Status: 401}, false},
		{"rate_limited", &APIError{Status: 429}, false},
		{"unknown", errors.New("forbidden"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if deliveryMethodsDenied(tc.err) != tc.want {
				t.Fatal("unrelated error was treated as method-specific denial")
			}
		})
	}
}

type deliveryMethodDenyTransport struct {
	base   http.RoundTripper
	denied *atomic.Int32
}

func (transport deliveryMethodDenyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path == orderMethodsPath {
		transport.denied.Add(1)
		return nil, &providercall.NotDispatchedError{Cause: providercall.ErrMethodForbidden}
	}
	return transport.base.RoundTrip(request)
}

func TestOrderMethodAdmissionDenialIsCachedAsPartialDiscovery(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != orderWarehousePath {
			t.Error("admission-denied method was dispatched")
		}
		writeTestResponse(t, w, `{"warehouses":[{"warehouse_id":7,"warehouse_type":"fbs","status":"created"}],"has_next":false}`)
	}))
	defer server.Close()
	reader := newOrderTestClient(t, server, OrderReadOptions{})
	var denied atomic.Int32
	reader.read.http.Transport = deliveryMethodDenyTransport{base: http.DefaultTransport, denied: &denied}
	for range 2 {
		discovery, err := reader.ListOrderDelivery(context.Background(), orderTestScope())
		if err != nil || discovery.Complete || len(discovery.Profiles) != 1 || discovery.Profiles[0].Validate() != nil || orderKnown(discovery.Profiles[0].Scheme) != "FBS" {
			t.Fatalf("method admission denial blocked fresh warehouse facts: %v", err)
		}
	}
	if denied.Load() != 1 {
		t.Fatal("cached denied method was retried")
	}
}

func TestOrderMethodDenialPreservesIndependentPostingFacts(t *testing.T) {
	t.Parallel()
	for _, scheme := range []string{"fbs", "rfbs", "fbo"} {
		t.Run(scheme, func(t *testing.T) {
			t.Parallel()
			var methods atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case orderGetPath:
					writeTestResponse(t, w, `{"result":{"posting_number":"p","order_id":1,"delivery_method":{"warehouse_id":7,"id":8},"products":[]}}`)
				case orderWarehousePath:
					writeTestResponse(t, w, fmt.Sprintf(`{"warehouses":[{"warehouse_id":7,"warehouse_type":%q,"status":"created"}],"has_next":false}`, scheme))
				case orderMethodsPath:
					methods.Add(1)
					w.WriteHeader(http.StatusForbidden)
					writeTestResponse(t, w, `{}`)
				}
			}))
			defer server.Close()
			reader := newOrderTestClient(t, server, OrderReadOptions{ResolveDelivery: true})
			wantScheme := map[string]string{"fbs": "FBS", "rfbs": "rFBS", "fbo": "FBO"}[scheme]
			for range 2 {
				posting, err := reader.ReadOrderPosting(context.Background(), orderTestScope(), "p")
				if err != nil || orderKnown(posting.Scheme) != wantScheme || posting.DeliveryProfile != nil || !hasOrderDrift(posting, "delivery_methods_access_denied") {
					t.Fatalf("method denial blocked posting or invented capabilities: %v", err)
				}
				if scheme == "fbo" && !hasOrderDrift(posting, "fbo_out_of_scope") {
					t.Fatal("FBO exclusion disappeared")
				}
			}
			discovery, err := reader.ListOrderDelivery(context.Background(), orderTestScope())
			if err != nil || discovery.Complete || len(discovery.Methods) != 0 || len(discovery.Profiles) != 1 || methods.Load() != 1 {
				t.Fatal("denied metadata was complete, lost or repeatedly requested")
			}
			profile := discovery.Profiles[0]
			if profile.Validate() != nil || profile.MethodVerified || profile.MethodID.IsKnown() || profile.CarrierIntegration.IsKnown() || profile.DestinationType.IsKnown() {
				t.Fatal("warehouse-only profile invalid or method-dependent fact invented")
			}
			decision := ordercontract.AssessCapability(ordercontract.CapabilityFacts{Scheme: *profile.Scheme.Value, WarehouseID: profile.WarehouseID, VerifiedDimensions: profile.VerifiedDimensions}, "pack")
			if decision.Status == "available" {
				t.Fatal("method denial unlocked fulfillment")
			}
		})
	}
}

func TestOrderDeliveryOtherFailuresRemainFatal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, body string
		status           int
	}{
		{"warehouse_forbidden", orderWarehousePath, `{}`, 403},
		{"method_unauthorized", orderMethodsPath, `{}`, 401},
		{"method_rate_limited", orderMethodsPath, `{}`, 429},
		{"method_unknown_failure", orderMethodsPath, `{}`, 500},
		{"method_malformed_json", orderMethodsPath, `{`, 200},
		{"method_malformed_payload", orderMethodsPath, `{"delivery_methods":[],"has_next":"false"}`, 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.path {
					w.WriteHeader(tc.status)
					writeTestResponse(t, w, tc.body)
					return
				}
				writeTestResponse(t, w, `{"warehouses":[{"warehouse_id":7,"warehouse_type":"fbs"}],"has_next":false}`)
			}))
			defer server.Close()
			reader := newOrderTestClient(t, server, OrderReadOptions{})
			if _, err := reader.ListOrderDelivery(context.Background(), orderTestScope()); err == nil {
				t.Fatal("independent failure was downgraded to optional method denial")
			}
		})
	}
}
