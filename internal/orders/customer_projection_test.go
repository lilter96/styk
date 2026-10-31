package orders

import "testing"

const (
	projectionNameID  = "11111111-1111-4111-8111-111111111111"
	projectionPhoneID = "22222222-2222-4222-8222-222222222222"
	projectionEmailID = "33333333-3333-4333-8333-333333333333"
)

func TestProjectCustomerUsesOnlyReviewedMappings(t *testing.T) {
	t.Parallel()
	customer := Customer{
		Name:  Present("Buyer Name"),
		Phone: Present("+70000000000"),
		Email: Present("buyer@example.test"),
		Address: Present(Address{
			Country: Present("RU"), Region: Present("Region"), City: Present("City"),
			Street: Present("Street"), House: Present("1"), Apartment: Present("2"), PostalCode: Present("123456"),
		}),
	}
	mappings := []PIIAttributeMapping{
		{Source: "customer.name", AttributeID: projectionNameID},
		{Source: "customer.phone", AttributeID: projectionPhoneID},
		{Source: "customer.address"},
	}
	attributes, address, err := ProjectCustomer(customer, mappings)
	if err != nil || len(attributes) != 2 {
		t.Fatalf("attributes=%+v err=%v", attributes, err)
	}
	if attributes[0].AttributeID != projectionNameID || *attributes[0].Value.Value != "Buyer Name" || attributes[1].AttributeID != projectionPhoneID {
		t.Fatalf("unexpected projection: %+v", attributes)
	}
	wantAddress := "RU, Region, City, Street, 1, 2, 123456"
	if !address.IsKnown() || *address.Value != wantAddress {
		t.Fatalf("address=%+v", address)
	}
}

func TestProjectCustomerOmitsMissingValuesAndRejectsDuplicates(t *testing.T) {
	t.Parallel()
	customer := Customer{Name: Field[string]{Presence: PresenceMissing}, Phone: Field[string]{Presence: PresenceNull}, Email: Present("buyer@example.test")}
	attributes, address, err := ProjectCustomer(customer, []PIIAttributeMapping{{Source: "customer.name", AttributeID: projectionNameID}, {Source: "customer.email", AttributeID: projectionEmailID}})
	if err != nil || len(attributes) != 1 || attributes[0].AttributeID != projectionEmailID || address.IsSpecified() {
		t.Fatalf("attributes=%+v address=%+v err=%v", attributes, address, err)
	}
	for _, mappings := range [][]PIIAttributeMapping{
		{{Source: "customer.name", AttributeID: projectionNameID}, {Source: "customer.phone", AttributeID: projectionNameID}},
		{{Source: "customer.address"}, {Source: "customer.address"}},
		{{Source: "customer.address", AttributeID: projectionEmailID}},
		{{Source: "customer.unknown"}},
	} {
		if _, _, err = ProjectCustomer(customer, mappings); err == nil {
			t.Fatalf("invalid projection admitted: %+v", mappings)
		}
	}
}

func TestConfigPIIMappingsRequireExplicitTargets(t *testing.T) {
	t.Parallel()
	base := Config{Scope: Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moysklad"}, Revision: 1, PIIEnabled: true}
	for _, test := range []struct {
		name    string
		mapping FieldMapping
		valid   bool
	}{
		{"address", FieldMapping{Source: "customer.address", Target: "shipment_address"}, true},
		{"name", FieldMapping{Source: "customer.name", Target: "attribute", AttributeID: projectionNameID}, true},
		{"address-as-attribute", FieldMapping{Source: "customer.address", Target: "attribute", AttributeID: projectionNameID}, false},
		{"name-as-address", FieldMapping{Source: "customer.name", Target: "shipment_address"}, false},
		{"non-customer-address", FieldMapping{Source: "order_number", Target: "shipment_address"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := base
			config.FieldMappings = []FieldMapping{test.mapping}
			err := config.validateFieldMappings()
			if test.valid && err != nil || !test.valid && err == nil {
				t.Fatalf("err=%v", err)
			}
		})
	}
	config := base
	config.PIIEnabled = false
	config.FieldMappings = []FieldMapping{{Source: "customer.address", Target: "shipment_address"}}
	if err := config.validateFieldMappings(); err != ErrBlocked {
		t.Fatalf("disabled PII mapping err=%v", err)
	}
}
