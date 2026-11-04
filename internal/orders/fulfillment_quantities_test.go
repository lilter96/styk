package orders

import (
	"slices"
	"testing"
)

func TestPartialCancellationRequiresExactApprovedReduction(t *testing.T) {
	t.Parallel()
	first, second := LineIdentity{SKU: "100", OfferID: "first"}, LineIdentity{SKU: "200", OfferID: "second"}
	before := Posting{Scope: Scope{Tenant: "merchant", OzonAccount: "ozon", MoyskladAccount: "moy"}, PostingNumber: "posting", CommercialOrderID: "order", WarehouseID: Present("warehouse"), DeliveryMethodID: Present("method"), Evidence: EvidenceRevision{Origin: OriginOzon}, Lines: []PostingLine{{Identity: first, Quantity: "5"}, {Identity: second, Quantity: "4"}}}
	for _, tc := range []struct {
		name   string
		change func(*Posting)
		lines  []ActionQuantity
		want   bool
	}{
		{"exact", func(p *Posting) { p.Lines[0].Quantity = "3" }, []ActionQuantity{{Identity: first, Quantity: "2"}}, true},
		{"full line removed", func(p *Posting) { p.Lines = p.Lines[1:] }, []ActionQuantity{{Identity: first, Quantity: "5"}}, true},
		{"not applied", func(*Posting) {}, []ActionQuantity{{Identity: first, Quantity: "2"}}, false},
		{"unrelated reduction", func(p *Posting) { p.Lines[0].Quantity = "3"; p.Lines[1].Quantity = "3" }, []ActionQuantity{{Identity: first, Quantity: "2"}}, false},
		{"reordered exact", func(p *Posting) { p.Lines[0].Quantity = "3"; slices.Reverse(p.Lines) }, []ActionQuantity{{Identity: first, Quantity: "2"}}, true},
		{"duplicate cancellation", func(p *Posting) { p.Lines[0].Quantity = "1" }, []ActionQuantity{{Identity: first, Quantity: "2"}, {Identity: first, Quantity: "2"}}, false},
		{"overflow", func(*Posting) {}, []ActionQuantity{{Identity: first, Quantity: "18446744073709551617"}}, false},
		{"tenant substitution", func(p *Posting) { p.Scope.Tenant = "other"; p.Lines[0].Quantity = "3" }, []ActionQuantity{{Identity: first, Quantity: "2"}}, false},
		{"missing rows", func(p *Posting) { p.Lines = nil }, []ActionQuantity{{Identity: first, Quantity: "2"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			after := before
			after.Lines = slices.Clone(before.Lines)
			tc.change(&after)
			if got := PartialCancellationObserved(before, after, tc.lines); got != tc.want {
				t.Fatalf("observed=%v wanted=%v", got, tc.want)
			}
		})
	}
}
