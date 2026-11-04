package orders

import (
	"slices"
	"testing"
	"time"
)

func TestSplitRequiresEveryExactPackageAndTarget(t *testing.T) {
	t.Parallel()
	identity := LineIdentity{SKU: "100", OfferID: "offer", ProviderLineID: "parent-line"}
	before := Posting{Scope: Scope{Tenant: "merchant", OzonAccount: "ozon", MoyskladAccount: "moy"}, CommercialOrderID: "order", PostingNumber: "parent", WarehouseID: Present("warehouse"), DeliveryMethodID: Present("method"), Evidence: EvidenceRevision{Revision: "source", Origin: OriginOzon, ObservedAt: time.Now().UTC()}, Lines: []PostingLine{{Identity: identity, Quantity: "2"}}}
	packages := []ActionPackage{{Lines: []ActionQuantity{{Identity: identity, Quantity: "1"}}}, {Lines: []ActionQuantity{{Identity: identity, Quantity: "1"}}}}
	children := make([]Posting, 2)
	for i, number := range []string{"child-a", "child-b"} {
		children[i] = before
		children[i].PostingNumber = number
		children[i].Status = Present("awaiting_deliver")
		children[i].Lines = []PostingLine{{Identity: LineIdentity{SKU: "100", OfferID: "offer", ProviderLineID: number}, Quantity: "1"}}
	}
	for _, tc := range []struct {
		name   string
		change func([]Posting) []Posting
		want   bool
	}{
		{"complete", func(p []Posting) []Posting { return p }, true},
		{"reordered", func(p []Posting) []Posting { slices.Reverse(p); return p }, true},
		{"missing child", func(p []Posting) []Posting { return p[:1] }, false},
		{"duplicate child", func(p []Posting) []Posting { p[1] = p[0]; return p }, false},
		{"wrong tenant", func(p []Posting) []Posting { p[0].Scope.Tenant = "other"; return p }, false},
		{"wrong warehouse", func(p []Posting) []Posting { p[0].WarehouseID = Present("other"); return p }, false},
		{"ship failure", func(p []Posting) []Posting { p[0].Substatus = Present("ship_failed"); return p }, false},
		{"not assembled", func(p []Posting) []Posting { p[0].Status = Present("awaiting_packaging"); return p }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual := tc.change(slices.Clone(children))
			if got := SplitObserved(before, packages, []string{"child-a", "child-b"}, actual); got != tc.want {
				t.Fatalf("got %v expected %v", got, tc.want)
			}
		})
	}
}
