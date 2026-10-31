package orderflow

import (
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestPostingTextCoversFinancialAnalyticsDeliveryAndRelations(t *testing.T) {
	t.Parallel()
	window := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	posting := orders.Posting{
		PostingNumber: "posting", CommercialOrderID: "order", OrderNumber: orders.Present("ORDER-1"),
		ParentPostingNumber: orders.Present("parent"), ChildPostingNumbers: []string{"child-2", "child-1"},
		Status: orders.Present("delivering"), Substatus: orders.Present("posting_transferring_to_delivery"),
		Financial: orders.FinancialFacts{Currency: orders.Present("RUB"), Commission: orders.Present(orders.Money{Amount: "12.34", Currency: "RUB"})},
		Analytics: orders.AnalyticsFacts{Region: orders.Present("Region"), Premium: orders.Present(true)},
		Delivery:  orders.DeliveryFacts{ProviderName: orders.Present("Carrier"), TrackingNumber: orders.Present("TRACK"), WindowFrom: orders.Present(window)},
	}
	for _, test := range []struct {
		source string
		want   string
	}{
		{"order_number", "ORDER-1"},
		{"parent_posting_number", "parent"},
		{"child_posting_numbers", "child-2, child-1"},
		{"substatus", "posting_transferring_to_delivery"},
		{"financial.currency", "RUB"},
		{"financial.commission", "12.34 RUB"},
		{"analytics.region", "Region"},
		{"analytics.premium", "true"},
		{"delivery.provider_name", "Carrier"},
		{"delivery.tracking_number", "TRACK"},
		{"delivery.window_from", "2026-10-31T12:00:00Z"},
	} {
		value, supported := postingText(posting, test.source)
		if !supported || !value.IsKnown() || *value.Value != test.want {
			t.Fatalf("%s=%+v supported=%v", test.source, value, supported)
		}
	}
	if _, supported := postingText(posting, "financial.unknown"); supported {
		t.Fatal("unknown financial source accepted")
	}
}

func TestMirrorWritePlansPIIMappingsWithoutPersistingValues(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	review.Config.PIIEnabled, review.Config.PIIRetentionSeconds = true, 3600
	review.Config.FieldMappings = append(review.Config.FieldMappings,
		orders.FieldMapping{Source: "customer.name", Target: "attribute", AttributeID: projectionAttributeID},
		orders.FieldMapping{Source: "customer.address", Target: "shipment_address"},
	)
	review.Metadata = orders.MetadataRegistry{Scope: review.Config.Scope, Revision: 1,
		Attributes: []orders.AttributeDefinition{{UniqueCode: "test", AttributeID: projectionAttributeID, Type: "string"}}, ObservedAt: flowTestNow}
	review.Postings[0].Customer = orders.ProtectedCustomer{Reference: "reference", Schema: "customer-v1", Revision: 1, Presence: orders.PresenceValue}
	reader := flowCustomerReader{customer: &orders.Customer{Name: orders.Present("Buyer"), Address: orders.Present(orders.Address{City: orders.Present("City")})}}
	write, validations := mirrorWrite(t.Context(), review.Postings[0], review, []orders.PositionWrite{{Identity: orders.LineIdentity{SKU: "sku"}, Mapping: orders.ProductMappingRef{MoyskladKind: "product"}, Quantity: "1", Price: orders.Present(orders.Money{Amount: "1", Currency: "RUB"})}}, reader)
	if len(validations) != 0 {
		t.Fatalf("validations=%+v", validations)
	}
	if len(write.PIIMappings) != 2 || !write.HasShipmentAddressMapping() || len(write.TransientAttributes) != 1 || *write.ShipmentAddress.Value != "City" {
		t.Fatalf("write=%+v", write)
	}
	if len(write.Attributes) != 0 {
		t.Fatal("transient PII leaked into durable attributes")
	}
}

const projectionAttributeID = "11111111-1111-4111-8111-111111111111"

type flowCustomerReader struct{ customer *orders.Customer }

func (reader flowCustomerReader) Customer(context.Context, orders.Scope, string, orders.ProtectedCustomer) (*orders.Customer, error) {
	return reader.customer, nil
}
