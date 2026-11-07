package ozon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrderDeliveryProviderIDSignedInt64Observation(t *testing.T) {
	t.Parallel()
	tests := []struct{ raw, want string }{
		{`-2`, "-2"}, {`"-2"`, "-2"}, {`0`, "0"}, {`""`, ""},
		{`"9007199254740995"`, "9007199254740995"},
		{`9223372036854775807`, "9223372036854775807"},
		{`-9223372036854775808`, "-9223372036854775808"},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			got, err := orderDeliveryProviderID([]byte(tc.raw))
			if err != nil || got != tc.want {
				t.Fatalf("signed provider identifier changed: got=%q error=%v", got, err)
			}
		})
	}
}

func TestOrderDeliveryProviderIDRejectsMalformedOrNoncanonicalValues(t *testing.T) {
	t.Parallel()
	tests := []string{
		`9223372036854775808`, `-9223372036854775809`, `2.5`, `2e0`,
		`"02"`, `"-02"`, `"-0"`, `"+2"`, `" 2"`, `"2 "`, `"x"`,
		`"2\n"`, `true`, `{}`, `[]`, `null`, ``,
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := orderDeliveryProviderID([]byte(raw)); !errors.Is(err, errOrderPayload) {
				t.Fatal("invalid provider identifier accepted", err)
			}
		})
	}
}

func TestOrderDeliveryProviderIDPreservesPresenceInSeparateNamespaces(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, fields, value string
		presence            orders.Presence
	}{
		{"missing", `{}`, "", orders.PresenceMissing},
		{"null", `{"provider_id":null,"tpl_provider_id":null}`, "", orders.PresenceNull},
		{"empty", `{"provider_id":"","tpl_provider_id":""}`, "", orders.PresenceEmpty},
		{"zero", `{"provider_id":0,"tpl_provider_id":0}`, "0", orders.PresenceValue},
		{"negative", `{"provider_id":-2,"tpl_provider_id":-2}`, "-2", orders.PresenceValue},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fields := newOrderFields([]byte(`{"delivery_method":` + tc.fields + `}`))
			var posting orders.Posting
			decodeOrderDelivery(fields, &posting)
			if *fields.err != nil || posting.Delivery.ProviderID.Presence != tc.presence || posting.Delivery.TPLProviderID.Presence != tc.presence {
				t.Fatal("delivery identifier presence changed", *fields.err)
			}
			if orderKnown(posting.Delivery.ProviderID) != tc.value || orderKnown(posting.Delivery.TPLProviderID) != tc.value {
				t.Fatal("observed delivery identifier changed")
			}
		})
	}
}

func TestOrderReaderHistoricalExpressSignedProviderIDRemainsOpaque(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/order_delivery_signed_provider_id.json")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeTestResponse(t, w, string(data))
	}))
	defer server.Close()
	reader := newOrderTestClient(t, server, OrderReadOptions{})
	posting, err := reader.ReadOrderPosting(context.Background(), orderTestScope(), "SYNTHETIC-historical-express-1")
	if err != nil || posting.Validate() != nil {
		t.Fatal("historical signed provider observation became unreadable", err)
	}
	if posting.Delivery.TPLProviderID.Presence != orders.PresenceValue || orderKnown(posting.Delivery.TPLProviderID) != "-2" || !posting.Delivery.ProviderID.IsMissing() {
		t.Fatal("negative provider observation was lost, aliased or converted to null")
	}
	if orderKnown(posting.WarehouseID) != "77" || orderKnown(posting.DeliveryMethodID) != "88" || !orderKnown(posting.Delivery.Express) || posting.DeliveryProfile != nil {
		t.Fatal("opaque provider identifier changed delivery capability")
	}
	graph, err := reader.RFBSOrderGraph(posting)
	if err != nil || len(graph) == 0 {
		t.Fatal("inspect signed provider observation", err)
	}
	for _, decision := range graph {
		if decision.Allowed {
			t.Fatal("historical Express observation granted a mutation")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("read inspection dispatched an additional request")
	}
}

func TestOrderReaderSignedProviderIDDoesNotRelaxRequiredIdentity(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/order_delivery_signed_provider_id.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []string{`"order_id": 1`, `"sku": 101`, `"warehouse_id": 77`, `"id": 88`}
	for _, field := range tests {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			key, _, found := strings.Cut(field, ":")
			if !found {
				t.Fatal("synthetic identity fixture missing delimiter")
			}
			body := strings.Replace(string(data), field, key+": -2", 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeTestResponse(t, w, body)
			}))
			defer server.Close()
			reader := newOrderTestClient(t, server, OrderReadOptions{})
			if _, err := reader.ReadOrderPosting(context.Background(), orderTestScope(), "SYNTHETIC-historical-express-1"); !errors.Is(err, errOrderPayload) {
				t.Fatal("negative required identity accepted", err)
			}
		})
	}
}
