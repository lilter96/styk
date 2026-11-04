package orders

import "testing"

func TestSplitReservePermissionDoesNotInventOrBroadenLineage(t *testing.T) {
	t.Parallel()
	scope := Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}
	permission := SplitReservePermission{Scope: scope, SplitWorkID: "confirmed-work", ParentPostingNumber: "parent", PostingNumber: "child"}
	for _, tc := range []struct {
		name  string
		alter func(*Posting)
		want  bool
	}{
		{"provider parent absent", func(*Posting) {}, true},
		{"provider parent agrees", func(p *Posting) { p.ParentPostingNumber = Present("parent") }, true},
		{"provider parent conflicts", func(p *Posting) { p.ParentPostingNumber = Present("other") }, false},
		{"other target", func(p *Posting) { p.PostingNumber = "other" }, false},
		{"other account", func(p *Posting) { p.Scope.OzonAccount = "other" }, false},
		{"other merchant", func(p *Posting) { p.Scope.Tenant = "other" }, false},
		{"nested split", func(p *Posting) { p.ChildPostingNumbers = []string{"grandchild"} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			posting := Posting{Scope: scope, PostingNumber: "child", Status: Present("awaiting_deliver"), Lines: []PostingLine{{Identity: LineIdentity{SKU: "sku"}, Quantity: "1"}}}
			tc.alter(&posting)
			write := CustomerOrderWrite{Applicable: Present(true), Positions: []PositionWrite{{Identity: LineIdentity{SKU: "sku"}, Quantity: "1"}}}
			validation := ApplySplitOpenQuantityReserve(posting, &write, PositionBaseline{}, permission)
			if (validation.Code == "") != tc.want || write.Positions[0].Reserve.IsKnown() != tc.want {
				t.Fatal("split allowance escaped exact target", validation)
			}
			if tc.name == "provider parent absent" && posting.ParentPostingNumber.IsKnown() {
				t.Fatal("local proof fabricated provider lineage")
			}
		})
	}
}
