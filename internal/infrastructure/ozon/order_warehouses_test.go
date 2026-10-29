package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/delivery"
	"github.com/lilter96/styk/internal/orders"
)

func TestFreshFBSClassificationWithoutPostingScheme(t *testing.T) {
	t.Parallel()
	var warehouses, methods atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case orderGetPath:
			writeTestResponse(t, w, `{"result":{"posting_number":"p","order_id":5,"status":"awaiting_packaging","delivery_method":{"warehouse_id":9007199254740993,"id":9007199254740993},"products":[]}}`)
		case orderWarehousePath:
			warehouses.Add(1)
			assertDeliveryRequest(t, request, `{"cursor":"","limit":100}`)
			writeTestResponse(t, w, `{"warehouses":[{"warehouse_id":9007199254740993,"warehouse_type":"fbs","status":"created","is_rfbs":false,"is_express":false}],"has_next":false,"cursor":""}`)
		case orderMethodsPath:
			methods.Add(1)
			assertDeliveryRequest(t, request, `{"filter":{},"limit":50,"offset":0}`)
			writeTestResponse(t, w, `{"delivery_methods":[],"has_next":false,"cursor":""}`)
		default:
			t.Error("unexpected provider endpoint")
		}
	}))
	defer server.Close()
	reader := newOrderTestClient(t, server, OrderReadOptions{ResolveDelivery: true})
	posting, err := reader.ReadOrderPosting(context.Background(), orderTestScope(), "p")
	if err != nil || orderKnown(posting.Scheme) != "FBS" || posting.DeliveryProfile != nil || orderKnown(posting.WarehouseID) != "9007199254740993" || hasOrderDrift(posting, "scheme_unverified") {
		t.Fatalf("fresh FBS not classified: %+v, %v", posting, err)
	}
	if warehouses.Load() != 1 || methods.Load() != 1 {
		t.Fatal("delivery discovery was not bounded")
	}
}

func assertDeliveryRequest(t *testing.T, request *http.Request, expected string) {
	t.Helper()
	var actual, want any
	if json.NewDecoder(request.Body).Decode(&actual) != nil || json.Unmarshal([]byte(expected), &want) != nil {
		t.Error("invalid discovery request")
		return
	}
	actualJSON, actualErr := json.Marshal(actual)
	wantJSON, wantErr := json.Marshal(want)
	if actualErr != nil || wantErr != nil || string(actualJSON) != string(wantJSON) {
		t.Error("discovery request differs from observed current request shape")
	}
}

func TestOrderDeliveryCacheScopeExpiryAndMethodIdentity(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path == orderWarehousePath {
			writeTestResponse(t, w, `{"warehouses":[{"warehouse_id":9007199254740993,"warehouse_type":"rfbs_express","status":"disabled","is_rfbs":true,"is_express":true}],"has_next":false}`)
			return
		}
		writeTestResponse(t, w, `{"delivery_methods":[{"id":9007199254740995,"warehouse_id":9007199254740993,"is_express":true}],"has_next":false}`)
	}))
	defer server.Close()
	reader := newOrderTestClient(t, server, OrderReadOptions{})
	checked := time.Now().UTC()
	reader.now = func() time.Time { return checked }
	runConcurrentDeliveryReads(t, reader)
	if calls.Load() != 2 {
		t.Fatalf("concurrent reads repeated discovery: %d", calls.Load())
	}
	other := orderTestScope()
	other.OzonAccount = "other"
	if _, err := reader.ListOrderDelivery(context.Background(), other); !errors.Is(err, orders.ErrInvalid) || calls.Load() != 2 {
		t.Fatal("cache crossed account scope")
	}
	checked = checked.Add(delivery.Validity)
	if _, err := reader.ListOrderDelivery(context.Background(), orderTestScope()); err != nil || calls.Load() != 4 {
		t.Fatal("expired cache reused")
	}
}

func runConcurrentDeliveryReads(t *testing.T, reader *OrdersClient) {
	t.Helper()
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			posting := orders.Posting{Scope: orderTestScope(), WarehouseID: orders.Present("9007199254740993"), DeliveryMethodID: orders.Present("9007199254740995")}
			profile, err := reader.ReadOrderDelivery(context.Background(), orderTestScope(), posting)
			if err != nil || orderKnown(profile.Scheme) != "rFBS" || !profile.MethodVerified || orderKnown(profile.MethodID) != "9007199254740995" || profile.CarrierIntegration.IsKnown() || profile.DestinationType.IsKnown() {
				t.Errorf("delivery identity or independent dimensions lost: %v", err)
			}
		})
	}
	group.Wait()
}

func TestOrderDeliveryNeverGuessesSchemeOrExpress(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case orderGetPath:
			writeTestResponse(t, w, `{"result":{"posting_number":"p","order_id":5,"delivery_method":{"warehouse_id":7,"id":8,"name":"FBS RFBS Express PVZ"},"products":[]}}`)
		case orderWarehousePath:
			writeTestResponse(t, w, `{"warehouses":[{"warehouse_id":7,"warehouse_type":"future_type","status":"created","name":"FBS rFBS Express PVZ"}],"has_next":false}`)
		case orderMethodsPath:
			writeTestResponse(t, w, `{"delivery_methods":[{"id":8,"warehouse_id":7,"name":"FBS rFBS Express PVZ"}],"has_next":false}`)
		}
	}))
	defer server.Close()
	posting, err := newOrderTestClient(t, server, OrderReadOptions{ResolveDelivery: true}).ReadOrderPosting(context.Background(), orderTestScope(), "p")
	if err != nil || posting.Scheme.IsKnown() || posting.DeliveryProfile != nil || !hasOrderDrift(posting, "unknown_enum") {
		t.Fatal("endpoint or names established delivery facts")
	}
}

func TestOrderDeliveryRejectsUnknownContinuation(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, response string }{
		{"missing_more", `{"delivery_methods":[]}`},
		{"unknown_next_page", `{"delivery_methods":[],"has_next":true,"cursor":"not_a_request_cursor"}`},
		{"duplicate_method", `{"delivery_methods":[{"id":8,"warehouse_id":7},{"id":8,"warehouse_id":7}],"has_next":false}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path == orderWarehousePath {
					writeTestResponse(t, w, `{"warehouses":[],"has_next":false}`)
					return
				}
				writeTestResponse(t, w, tc.response)
			}))
			defer server.Close()
			_, err := newOrderTestClient(t, server, OrderReadOptions{}).ListOrderDelivery(context.Background(), orderTestScope())
			if err == nil || calls.Load() != 2 {
				t.Fatal("unverified method pagination guessed or malformed response accepted")
			}
		})
	}
}

func TestExplicitFBOIsExcludedBeforeDiscovery(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != orderGetPath {
			t.Error("FBO triggered delivery workflow discovery")
		}
		writeTestResponse(t, w, `{"result":{"posting_number":"p","order_id":5,"is_fbo":true,"delivery_method":{"warehouse_id":7,"id":8},"products":[]}}`)
	}))
	defer server.Close()
	posting, err := newOrderTestClient(t, server, OrderReadOptions{ResolveDelivery: true}).ReadOrderPosting(context.Background(), orderTestScope(), "p")
	if err != nil || orderKnown(posting.Scheme) != "FBO" || posting.DeliveryProfile != nil {
		t.Fatal("FBO was enabled")
	}
	encoded, err := json.Marshal(posting)
	if err != nil || strings.Contains(string(encoded), "carrier_pvz") {
		t.Fatal("destination facts fabricated")
	}
}
