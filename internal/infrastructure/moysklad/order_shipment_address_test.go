package moysklad

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrderCreatesNativeStructuredAddressWithoutCountryInference(t *testing.T) {
	t.Parallel()
	simulator, client := newOrderSimulator(t, "ok")
	command := simulator.command
	command.CustomerOrder.PIIMappings = []orders.PIIAttributeMapping{{Source: "customer.address.city"}, {Source: "customer.address.street"}, {Source: "customer.address.house"}}
	command.ProtectedCustomer = &orders.ProtectedCustomer{Reference: "protected-reference", Schema: "customer-v1", Revision: 1, Presence: orders.PresenceValue}
	command.PII = &orders.Customer{Address: orders.Present(orders.Address{City: orders.Present("SYNTHETIC-CITY"), Street: orders.Present("SYNTHETIC-STREET"), House: orders.Present("1"), Country: orders.Present("NOT-A-COUNTRY-REFERENCE")})}
	var calls atomic.Int32
	if err := client.BindOrderIntent(command, func(context.Context) (bool, error) { calls.Add(1); return true, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteOrder(context.Background(), command)
	if err != nil || receipt.State != orders.StateAcknowledged || calls.Load() != 1 {
		t.Fatal("structured order create failed", err)
	}
	body := <-simulator.captured
	full, ok := body["shipmentAddressFull"].(map[string]any)
	if !ok || full["city"] != "SYNTHETIC-CITY" || full["street"] != "SYNTHETIC-STREET" || full["house"] != "1" {
		t.Fatal("native structured values absent")
	}
	if _, ok = full["country"]; ok {
		t.Fatal("country text became native reference")
	}
	if _, ok = body["shipmentAddress"]; ok {
		t.Fatal("ambiguous flat and structured address write")
	}
	durable, err := json.Marshal(command)
	if err != nil || strings.Contains(string(durable), "SYNTHETIC") {
		t.Fatal("structured customer values leaked into intent")
	}
}
func TestStructuredAddressReaderValidatesReferencesAndPresence(t *testing.T) {
	t.Parallel()
	_, client := newOrderSimulator(t, "ok")
	country := client.reference("entity/country", "country", orderTestID(70))
	raw, err := json.Marshal(map[string]any{"city": "SYNTHETIC-CITY", "house": nil, "apartment": "", "country": country})
	if err != nil {
		t.Fatal(err)
	}
	address, err := client.readShipmentAddressFull(raw)
	if err != nil || !address.IsKnown() || address.Value.House.Presence != orders.PresenceNull || address.Value.Apartment.Presence != orders.PresenceEmpty || *address.Value.CountryID.Value != orderTestID(70) {
		t.Fatal("structured native presence lost", err)
	}
	for _, bad := range []string{`{"country":{"meta":{"href":"http://127.0.0.1/entity/country/` + orderTestID(70) + `","type":"country"}}}`, `{"futurePIIField":"do-not-discard"}`} {
		if _, err = client.readShipmentAddressFull([]byte(bad)); err == nil {
			t.Fatal("unsafe or unknown native address accepted")
		}
	}
	if address, err = client.readShipmentAddressFull([]byte("null")); err != nil || address.Presence != orders.PresenceNull {
		t.Fatal("null became empty address")
	}
}

func newStructuredOrderSimulator(t *testing.T) (*orderSimulator, *OrderClient, *atomic.Value) {
	t.Helper()
	simulator, _ := newOrderSimulator(t, "ok")
	address := &atomic.Value{}
	address.Store(map[string]any{"city": "SYNTHETIC-OLD-CITY", "street": "SYNTHETIC-UNOWNED-STREET", "addInfo": "SYNTHETIC-UNOWNED-DETAIL", "country": simulator.reference("entity/country", "country", 70)})
	original := simulator.server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/entity/customerorder/"+orderTestID(9)) {
			simulator.details.Add(1)
			body := simulator.document()
			body["shipmentAddressFull"] = address.Load()
			simulator.reply(w, body)
			return
		}
		simulator.serveHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	simulator.server = server
	// All references in the fixture must use the same independently validated origin.
	value := address.Load().(map[string]any)
	value["country"] = simulator.reference("entity/country", "country", 70)
	address.Store(value)
	original.Close()
	base, err := New(Options{BaseURL: server.URL + "/api/remap/1.2/", Token: "synthetic-token", Timeout: time.Second, Interval: time.Nanosecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewOrderClient(base, simulator.command.Scope)
	if err != nil {
		t.Fatal(err)
	}
	return simulator, client, address
}
func TestStructuredAddressUpdateMergesUnselectedNativeMembers(t *testing.T) {
	t.Parallel()
	simulator, client, _ := newStructuredOrderSimulator(t)
	command := simulator.command
	command.Correlation.CustomerOrderID = orderTestID(9)
	actual, err := client.ReadCustomerOrder(context.Background(), command.Correlation)
	if err != nil {
		t.Fatal(err)
	}
	command.Action = orders.ActionMirrorUpdate
	command.ExpectedTargetRevision = actual.Revision
	command.CustomerOrder.OwnedFields = []string{"shipment_address_full.city"}
	command.CustomerOrder.PIIMappings = []orders.PIIAttributeMapping{{Source: "customer.address.city"}}
	command.ProtectedCustomer = &orders.ProtectedCustomer{Reference: "protected-reference", Schema: "customer-v1", Revision: 1, Presence: orders.PresenceValue}
	command.PII = &orders.Customer{Address: orders.Present(orders.Address{City: orders.Present("SYNTHETIC-NEW-CITY")})}
	if err = client.BindOrderIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	receipt, err := client.WriteOrder(context.Background(), command)
	if err != nil || receipt.State != orders.StateAcknowledged {
		t.Fatal("native address update failed", err)
	}
	full := (<-simulator.captured)["shipmentAddressFull"].(map[string]any)
	if full["city"] != "SYNTHETIC-NEW-CITY" || full["street"] != "SYNTHETIC-UNOWNED-STREET" || full["addInfo"] != "SYNTHETIC-UNOWNED-DETAIL" || full["country"] == nil {
		t.Fatal("unselected native address fields erased")
	}
}
func TestStructuredAddressUpdateRejectsManualRevisionChange(t *testing.T) {
	t.Parallel()
	simulator, client, address := newStructuredOrderSimulator(t)
	command := simulator.command
	command.Correlation.CustomerOrderID = orderTestID(9)
	actual, err := client.ReadCustomerOrder(context.Background(), command.Correlation)
	if err != nil {
		t.Fatal(err)
	}
	command.Action = orders.ActionMirrorUpdate
	command.ExpectedTargetRevision = actual.Revision
	command.CustomerOrder.OwnedFields = []string{"shipment_address_full.city"}
	command.CustomerOrder.PIIMappings = []orders.PIIAttributeMapping{{Source: "customer.address.city"}}
	command.ProtectedCustomer = &orders.ProtectedCustomer{Reference: "protected-reference", Schema: "customer-v1", Revision: 1, Presence: orders.PresenceValue}
	command.PII = &orders.Customer{Address: orders.Present(orders.Address{City: orders.Present("SYNTHETIC-NEW-CITY")})}
	if err = client.BindOrderIntent(command, func(context.Context) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	address.Store(map[string]any{"city": "SYNTHETIC-MANUAL-EDIT"})
	if _, err = client.WriteOrder(context.Background(), command); err == nil || simulator.writes.Load() != 0 {
		t.Fatal("manual address change was overwritten")
	}
}
