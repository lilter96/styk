package orders

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestShipmentAddressExplicitComponentsPreserveUnknownAndPrivacy(t *testing.T) {
	t.Parallel()
	customer := Customer{Address: Present(Address{City: Present("SYNTHETIC-CITY"), House: Field[string]{Presence: PresenceNull}, Street: Present("SYNTHETIC-STREET"), Country: Present("NOT-A-COUNTRY-UUID")})}
	write := CustomerOrderWrite{PIIMappings: []PIIAttributeMapping{{Source: "customer.address.city"}, {Source: "customer.address.house"}}}
	if err := write.ApplyCustomerProjection(customer); err != nil {
		t.Fatal(err)
	}
	if !write.ShipmentAddressFull.IsKnown() || !write.ShipmentAddressFull.Value.City.IsKnown() || write.ShipmentAddressFull.Value.House.IsSpecified() || write.ShipmentAddressFull.Value.CountryID.IsKnown() || write.ShipmentAddressFull.Value.Street.IsSpecified() {
		t.Fatal("unselected, null or unsupported values were inferred")
	}
	raw, err := json.Marshal(write)
	if err != nil || strings.Contains(string(raw), "SYNTHETIC") {
		t.Fatal("address leaked to durable command")
	}
	target := TargetObservation{Fields: write}
	raw, err = json.Marshal(target)
	if err != nil || strings.Contains(string(raw), "SYNTHETIC") || !strings.Contains(string(raw), "fields.shipment_address_full") {
		t.Fatal("native readback leaked to public target")
	}
}
func TestShipmentAddressBoundsAreExplicitAndNoSilentTruncation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		key     string
		maximum int
	}{{"city", 255}, {"street", 255}, {"house", 30}, {"apartment", 30}, {"postal_code", 6}} {
		t.Run(test.key, func(t *testing.T) {
			a := ShipmentAddress{}
			a.Set(test.key, Present(strings.Repeat("я", test.maximum)))
			if a.Validate() != nil {
				t.Fatal("valid UTF-8 character length rejected")
			}
			a.Set(test.key, Present(strings.Repeat("я", test.maximum+1)))
			var invalid *ShipmentAddressValueError
			if !errors.As(a.Validate(), &invalid) || invalid.Field != test.key || invalid.Code != "shipment_address_component_too_long" {
				t.Fatal("overflow not diagnosed by component")
			}
		})
	}
}
func TestShipmentAddressMappingRequiresPIIAndAvoidsFlatCollision(t *testing.T) {
	t.Parallel()
	config := Config{Scope: Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}, Revision: 1}
	config.PIIEnabled = true
	config.PIIRetentionSeconds = 3600
	config.FieldMappings = []FieldMapping{{Source: "customer.address.city", Target: "shipment_address_full.city"}}
	if err := config.validateFieldMappings(); err != nil {
		t.Fatal(err)
	}
	config.PIIEnabled = false
	if config.validateFieldMappings() == nil {
		t.Fatal("PII opt-in bypassed")
	}
	config.PIIEnabled = true
	config.FieldMappings = append(config.FieldMappings, FieldMapping{Source: "customer.address", Target: "shipment_address"})
	if config.validateFieldMappings() == nil {
		t.Fatal("both native address representations accepted")
	}
	if ShipmentAddressTarget("customer.address.country") != "" {
		t.Fatal("country free text became native UUID")
	}
}
