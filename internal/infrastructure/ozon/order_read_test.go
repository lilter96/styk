package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func orderTestScope() orders.Scope {
	return orders.Scope{Tenant: "orders-tenant", OzonAccount: "id", MoyskladAccount: "moy-account"}
}

func newOrderTestClient(t *testing.T, server *httptest.Server, options OrderReadOptions) *OrdersClient {
	t.Helper()
	client := newTestClient(t, server)
	client.id, client.attempts = "id", 1
	reader, err := NewOrderClientWithOptions(client, orderTestScope(), options)
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func TestOrderReaderExactIdentityPresenceAndPrivacy(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestResponse(t, w, `{"result":{"posting_number":"posting-1","order_id":18446744073709551615,"order_number":"commercial-1","status":"future_status","substatus":null,"scheme":"FBS","delivery_method":{"warehouse_id":"1020001323428000","id":9007199254740993},"products":[{"sku":9007199254740993,"offer_id":"offer-1","quantity":2,"price":"1234567890123456.78","currency_code":"RUB","name":"","vat":"0"}],"cancellation":{"cancel_reason":null,"cancel_reason_id":0,"cancelled_after_ship":false},"customer":{"name":"PRIVATE_NAME","phone":"PRIVATE_PHONE","address":{"country":"PRIVATE_COUNTRY","address_tail":"PRIVATE_ADDRESS"},"PRIVATE_KEY":"PRIVATE_VALUE"},"financial_data":{"products":[{"product_id":"7777","price":"20.05","old_price":25.1,"currency_code":"RUB","commission_amount":"0.00","commissions_currency_code":"RUB","payout":"18.05"}]},"unknown_PRIVATE_KEY":"PRIVATE_VALUE"}}`)
	}))
	defer server.Close()
	reader := newOrderTestClient(t, server, OrderReadOptions{CustomerPII: true})
	posting, err := reader.ReadOrderPosting(context.Background(), orderTestScope(), "posting-1")
	if err != nil {
		t.Fatal(err)
	}
	if posting.CommercialOrderID != "18446744073709551615" || orderKnown(posting.DeliveryMethodID) != "9007199254740993" || posting.Lines[0].Identity.SKU != "9007199254740993" {
		t.Fatal("large identifiers lost precision")
	}
	if posting.Lines[0].ProductID.IsKnown() || orderKnown(posting.Financial.Lines[0].ProviderProductID) != "7777" {
		t.Fatal("SKU and product ID namespaces were conflated")
	}
	if posting.Substatus.Presence != orders.PresenceNull || posting.Lines[0].Name.Presence != orders.PresenceEmpty || posting.Lines[0].VAT.Presence != orders.PresenceZero {
		t.Fatal("source presence was flattened")
	}
	if posting.Cancellation.Value.AfterShipment.Presence != orders.PresenceZero || posting.Cancellation.Value.ReasonPresence != orders.PresenceNull {
		t.Fatal("cancellation presence was flattened")
	}
	if posting.Lines[0].Price.Value.Amount != "1234567890123456.78" || posting.Financial.Lines[0].OldPrice.Value.Amount != "25.1" || posting.Financial.Lines[0].Commission.Presence != orders.PresenceZero {
		t.Fatal("money was rounded or reclassified")
	}
	if posting.PII == nil || orderKnown(posting.PII.Name) != "PRIVATE_NAME" {
		t.Fatal("authorized transient customer data missing")
	}
	encoded, err := json.Marshal(posting)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "PRIVATE") {
		t.Fatal("PII or unknown values escaped into sanitized facts")
	}
	if posting.Evidence.ProviderUpdatedAt.IsSpecified() || posting.Evidence.ProviderVersion.IsSpecified() {
		t.Fatal("provider update evidence was invented")
	}
	if !hasOrderDrift(posting, "unknown_enum") || !hasOrderDrift(posting, "schema_unverified") || !hasOrderDrift(posting, "unknown_key") {
		t.Fatal("schema drift was dropped")
	}
}

func TestOrderReaderSinglePageAndFreshDetail(t *testing.T) {
	t.Parallel()
	var lists, details atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if request.URL.Path == orderGetPath {
			details.Add(1)
			writeTestResponse(t, w, `{"result":{"posting_number":"posting-1","order_id":"5","products":[{"offer_id":"offer-1","quantity":1}]}}`)
			return
		}
		lists.Add(1)
		if request.URL.Path != orderListPath || string(body["cursor"]) != `"cursor-0"` {
			t.Error("incorrect list endpoint or cursor")
		}
		var flags OrderReadOptions
		if err := json.Unmarshal(body["with"], &flags); err != nil {
			t.Error(err)
		}
		if !flags.AnalyticsData || !flags.FinancialData || !flags.Barcodes || flags.LegalInfo {
			t.Error("request flags changed")
		}
		writeTestResponse(t, w, `{"postings":[{"posting_number":"posting-1"}],"has_next":true,"cursor":"cursor-1"}`)
	}))
	defer server.Close()
	reader := newOrderTestClient(t, server, OrderReadOptions{AnalyticsData: true, FinancialData: true, Barcodes: true})
	now := time.Now().UTC()
	page, err := reader.ReadOrderPage(context.Background(), orderTestScope(), orders.OrderWindow{Stream: "ozon-orders-discovery", From: now.Add(-24 * time.Hour), To: now, Limit: 10}, "cursor-0")
	if err != nil {
		t.Fatal(err)
	}
	if lists.Load() != 1 || details.Load() != 1 || page.Complete || page.NextCursor != "cursor-1" || len(page.Postings) != 1 {
		t.Fatal("one-page bound or detail read violated")
	}
}

func TestOrderReaderRejectsMalformedPagesWithoutRetry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, response string
		status         int
	}{
		{"legacy wrapper", `{"result":{"postings":[],"has_next":false}}`, 200},
		{"missing continuation", `{"postings":[{"posting_number":"p"}],"has_next":true}`, 200},
		{"repeated cursor", `{"postings":[{"posting_number":"p"}],"has_next":true,"cursor":"repeat"}`, 200},
		{"empty continuation", `{"postings":[],"has_next":true,"cursor":"next"}`, 200},
		{"duplicate keys", `{"postings":[],"postings":[],"has_next":false}`, 200},
		{"rate limit", `{}`, 429},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				writeTestResponse(t, w, tc.response)
			}))
			defer server.Close()
			reader := newOrderTestClient(t, server, OrderReadOptions{})
			now := time.Now().UTC()
			_, err := reader.ReadOrderPage(context.Background(), orderTestScope(), orders.OrderWindow{Stream: "ozon-orders-discovery", From: now.Add(-time.Hour), To: now, Limit: 10}, "repeat")
			if err == nil || calls.Load() != 1 {
				t.Fatal("malformed page accepted or request retried")
			}
		})
	}
}

func TestOrderReaderScopeAndMutationBoundaries(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); writeTestResponse(t, w, `{}`) }))
	defer server.Close()
	reader := newOrderTestClient(t, server, OrderReadOptions{})
	other := orderTestScope()
	other.Tenant = "other"
	if _, err := reader.ReadOrderPosting(context.Background(), other, "p"); !errors.Is(err, orders.ErrInvalid) {
		t.Fatal("cross-tenant read allowed")
	}
	if _, err := reader.WriteOrder(context.Background(), orders.Command{}); !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("read-only writer allowed")
	}
	if _, err := reader.ReadOrderCorrelation(context.Background(), orders.Correlation{}); !errors.Is(err, ErrOrderUnsupported) {
		t.Fatal("unsupported correlation accepted")
	}
	reader.ReadonlyReaderRequest = false
	if _, err := reader.ReadOrderPosting(context.Background(), orderTestScope(), "p"); !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("read-only request guard bypassed")
	}
	if calls.Load() != 0 {
		t.Fatal("blocked operation sent HTTP")
	}
}

func TestOrderReaderKeepsFBOAndUnknownTimeAsReadEvidence(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestResponse(t, w, `{"result":{"posting_number":"p","order_id":5,"is_fbo":true,"in_process_at":"2026-10-30 03:00:00.000","shipment_date":"","products":[]}}`)
	}))
	defer server.Close()
	reader := newOrderTestClient(t, server, OrderReadOptions{})
	posting, err := reader.ReadOrderPosting(context.Background(), orderTestScope(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if orderKnown(posting.Scheme) != "FBO" || posting.DeliveryProfile != nil || !hasOrderDrift(posting, "fbo_out_of_scope") {
		t.Fatal("FBO workflow was inferred")
	}
	if posting.Times.InProcessAt.IsKnown() || !hasOrderDrift(posting, "unverified_timestamp") || posting.Times.ShipmentAt.Presence != orders.PresenceEmpty {
		t.Fatal("ambiguous source time became an event timestamp")
	}
}

func hasOrderDrift(posting orders.Posting, code string) bool {
	for _, issue := range posting.Drift {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestOrderReaderRetainsAmountWithoutCurrencyAndGroupPresence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, groups string
		want         orders.Presence
	}{
		{"missing", ``, orders.PresenceMissing},
		{"null", `,"analytics_data":null,"delivery_method":null,"financial_data":null`, orders.PresenceNull},
		{"empty", `,"analytics_data":{},"delivery_method":{},"financial_data":{}`, orders.PresenceEmpty},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeTestResponse(t, w, `{"result":{"posting_number":"p","order_id":5,"products":[]`+tc.groups+`}}`)
			}))
			defer server.Close()
			posting, err := newOrderTestClient(t, server, OrderReadOptions{}).ReadOrderPosting(context.Background(), orderTestScope(), "p")
			if err != nil {
				t.Fatal(err)
			}
			if posting.Financial.Presence != tc.want || posting.Analytics.Presence != tc.want || posting.Delivery.Presence != tc.want {
				t.Fatal("group presence was flattened")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestResponse(t, w, `{"result":{"posting_number":"p","order_id":5,"products":[],"financial_data":{"products":[{"product_id":9,"price":"20.0500"}]}}}`)
	}))
	defer server.Close()
	posting, err := newOrderTestClient(t, server, OrderReadOptions{}).ReadOrderPosting(context.Background(), orderTestScope(), "p")
	if err != nil {
		t.Fatal(err)
	}
	line := posting.Financial.Lines[0]
	if line.Price.IsKnown() || orderKnown(line.PriceAmount) != "20.0500" || !hasOrderDrift(posting, "missing_currency") {
		t.Fatal("missing currency invented RUB or discarded exact amount")
	}
}
