package orders

import (
	"testing"
	"time"
)

func statusReadbackValues(scheme, carrier, status string) (Command, Posting) {
	now := time.Now().UTC()
	capability := CapabilityRef{ID: "profile", Revision: 1, WarehouseID: "warehouse", DeliveryMethodID: "method", Scheme: scheme,
		Carrier: Present(carrier), Express: Present(false), VerifiedDimensions: []string{"scheme", "warehouse", "carrier", "express"}, ObservedAt: now, ValidUntil: now.Add(time.Hour)}
	identity := LineIdentity{SKU: "123", OfferID: "offer"}
	command := Command{Scope: Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}, PostingNumber: "posting", CommercialOrderID: "order", Action: ActionPostingPack,
		PostingAction: &PostingAction{Capability: capability, Parameters: ActionParameters{Packages: []ActionPackage{{Lines: []ActionQuantity{{Identity: identity, Quantity: "2"}}}}}}}
	posting := Posting{Scope: command.Scope, PostingNumber: command.PostingNumber, CommercialOrderID: command.CommercialOrderID, Scheme: Present(scheme),
		WarehouseID: Present("warehouse"), DeliveryMethodID: Present("method"), Status: Present(status), Delivery: DeliveryFacts{IntegrationType: Present(carrier)}, Lines: []PostingLine{{Identity: identity, Quantity: "2"}}}
	return command, posting
}

func TestFulfillmentReadbackUsesSchemeSpecificSuccessors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, scheme, carrier, status string
		action                        Action
		want                          bool
	}{
		{"FBS exact ready", "FBS", "ozon", "awaiting_deliver", ActionPostingPack, true},
		{"FBS rapid delivery", "FBS", "ozon", "delivered", ActionPostingPack, true},
		{"FBS acceptance in progress confirms assembly only", "FBS", "ozon", "acceptance_in_progress", ActionPostingPack, true},
		{"FBS registration is not aggregator", "FBS", "ozon", "awaiting_registration", ActionPostingPack, false},
		{"aggregator registration", "rFBS", "aggregator", "awaiting_registration", ActionPostingPack, true},
		{"ambiguous documented spelling", "rFBS", "aggregator", "awaiting_delivery", ActionPostingPack, false},
		{"aggregator delivery observation", "rFBS", "aggregator", "delivering", ActionPostingPack, true},
		{"nonintegrated registration forbidden", "rFBS", "non_integrated", "awaiting_registration", ActionPostingPack, false},
		{"nonintegrated later delivery", "rFBS", "non_integrated", "driver_pickup", ActionPostingPack, true},
		{"nonintegrated ship later delivery", "rFBS", "non_integrated", "delivered", ActionPostingShip, true},
		{"nonintegrated last-mile later delivered", "rFBS", "non_integrated", "delivered", ActionPostingLastMile, true},
		{"carrier-owned ship is not seller command", "rFBS", "aggregator", "delivered", ActionPostingShip, false},
		{"unknown route", "rFBS", "unknown", "delivered", ActionPostingPack, false},
		{"hybrid route", "rFBS", "hybrid", "delivered", ActionPostingPack, false},
		{"cancellation does not prove packing", "FBS", "ozon", "cancelled", ActionPostingPack, false},
		{"not accepted is unresolved", "FBS", "ozon", "not_accepted", ActionPostingPack, false},
		{"unknown future state", "FBS", "ozon", "new_future_status", ActionPostingPack, false},
		{"cancel exact", "FBS", "ozon", "cancelled", ActionPostingCancel, true},
		{"cancel cannot advance to delivered", "FBS", "ozon", "delivered", ActionPostingCancel, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command, posting := statusReadbackValues(tc.scheme, tc.carrier, tc.status)
			command.Action = tc.action
			if got := FulfillmentStatusConfirmed(command, posting); got != tc.want {
				t.Fatalf("confirmed=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestFulfillmentAdvancedReadbackDoesNotBorrowConflictingEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*Command, *Posting)
	}{
		{"failed assembly", func(_ *Command, p *Posting) { p.Substatus = Present("ship_failed") }},
		{"registration error", func(_ *Command, p *Posting) { p.Substatus = Present("posting_registration_error") }},
		{"different quantity", func(_ *Command, p *Posting) { p.Lines[0].Quantity = "1" }},
		{"different tenant", func(_ *Command, p *Posting) { p.Scope.Tenant = "other" }},
		{"different warehouse", func(_ *Command, p *Posting) { p.WarehouseID = Present("other") }},
		{"different method", func(_ *Command, p *Posting) { p.DeliveryMethodID = Present("other") }},
		{"different scheme", func(_ *Command, p *Posting) { p.Scheme = Present("FBS") }},
		{"Express", func(c *Command, _ *Posting) { c.PostingAction.Capability.Express = Present(true) }},
		{"unspecified Express", func(c *Command, _ *Posting) { c.PostingAction.Capability.Express = Field[bool]{} }},
		{"conflicting integration", func(_ *Command, p *Posting) { p.Drift = []Drift{{Code: "conflicting_integration"}} }},
		{"missing capability", func(c *Command, _ *Posting) { c.PostingAction = nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command, posting := statusReadbackValues("rFBS", "aggregator", "delivered")
			tc.change(&command, &posting)
			if FulfillmentStatusConfirmed(command, posting) {
				t.Fatal("conflicting or missing evidence reconciled")
			}
		})
	}
}
