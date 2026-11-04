package orders

import "testing"

func TestOpenQuantityReserveUsesExactIdentityAndDecimal(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"awaiting_registration", "awaiting_approve", "awaiting_packaging", "awaiting_deliver"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			posting := Posting{Status: Present(status), Lines: []PostingLine{{Identity: LineIdentity{SKU: "sku-a"}, Quantity: "9007199254740993"}, {Identity: LineIdentity{SKU: "sku-b"}, Quantity: "1.25"}}}
			write := CustomerOrderWrite{Applicable: Present(true), Positions: []PositionWrite{{Identity: LineIdentity{SKU: "sku-b"}, Quantity: "1.25"}, {Identity: LineIdentity{SKU: "sku-a"}, Quantity: "9007199254740993"}}}
			if validation := ApplyOpenQuantityReserve(posting, &write, PositionBaseline{}); validation.Code != "" {
				t.Fatalf("pending reserve blocked: %+v", validation)
			}
			if !write.Positions[0].Reserve.IsKnown() || *write.Positions[0].Reserve.Value != "1.25" || *write.Positions[1].Reserve.Value != "9007199254740993" {
				t.Fatal("reserve lost exact quantity or matched array index")
			}
		})
	}
}

func TestOpenQuantityReservePreservesUnknownAndDoesNotReleaseUnownedInventory(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		alter func(*Posting, *CustomerOrderWrite)
		code  string
	}{
		{"missing status", func(p *Posting, _ *CustomerOrderWrite) { p.Status = Field[string]{} }, "reservation_policy_status_unknown"},
		{"unknown new status", func(p *Posting, _ *CustomerOrderWrite) { p.Status = Present("future_status") }, "reservation_policy_status_not_pending"},
		{"delivered", func(p *Posting, _ *CustomerOrderWrite) { p.Status = Present("delivered") }, "reservation_policy_status_not_pending"},
		{"delivering", func(p *Posting, _ *CustomerOrderWrite) { p.Status = Present("delivering") }, "reservation_policy_status_not_pending"},
		{"cancelled after physical shipment", func(p *Posting, _ *CustomerOrderWrite) {
			p.Status = Present("cancelled")
			p.Cancellation = Present(Cancellation{AfterShipment: Present(true)})
		}, "reservation_policy_shipment_or_cancellation_conflict"},
		{"cancelled without owned baseline", func(p *Posting, _ *CustomerOrderWrite) { p.Status = Present("cancelled") }, "reservation_policy_owned_release_required"},
		{"unposted", func(_ *Posting, w *CustomerOrderWrite) { w.Applicable = Present(false) }, "reservation_policy_requires_applicable_true"},
		{"malformed applicable", func(_ *Posting, w *CustomerOrderWrite) { w.Applicable = Field[bool]{Presence: PresenceValue} }, "reservation_policy_requires_applicable_true"},
		{"malformed cancellation", func(p *Posting, _ *CustomerOrderWrite) {
			p.Cancellation = Present(Cancellation{AfterShipment: Field[bool]{Presence: PresenceValue}})
		}, "reservation_policy_shipment_or_cancellation_conflict"},
		{"unknown applicable", func(_ *Posting, w *CustomerOrderWrite) { w.Applicable = Field[bool]{} }, "reservation_policy_requires_applicable_true"},
		{"partial cancellation", func(p *Posting, _ *CustomerOrderWrite) { p.Lines[0].CancelledQuantity = Present(Decimal("1")) }, "reservation_policy_partial_cancellation_unknown"},
		{"null cancelled quantity", func(p *Posting, _ *CustomerOrderWrite) {
			p.Lines[0].CancelledQuantity = Field[Decimal]{Presence: PresenceNull}
		}, "reservation_policy_partial_cancellation_unknown"},
		{"split parent", func(p *Posting, _ *CustomerOrderWrite) { p.ChildPostingNumbers = []string{"child"} }, "reservation_policy_split_lineage_requires_review"},
		{"split child", func(p *Posting, _ *CustomerOrderWrite) { p.ParentPostingNumber = Present("parent") }, "reservation_policy_split_lineage_requires_review"},
		{"different line quantity", func(_ *Posting, w *CustomerOrderWrite) { w.Positions[0].Quantity = "3" }, "reservation_policy_line_identity_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			posting := Posting{Status: Present("awaiting_packaging"), Lines: []PostingLine{{Identity: LineIdentity{SKU: "sku"}, Quantity: "2"}}}
			write := CustomerOrderWrite{Applicable: Present(true), Positions: []PositionWrite{{Identity: LineIdentity{SKU: "sku"}, Quantity: "2"}}}
			tc.alter(&posting, &write)
			if validation := ApplyOpenQuantityReserve(posting, &write, PositionBaseline{}); validation.Code != tc.code {
				t.Fatalf("validation=%+v want=%s", validation, tc.code)
			}
			if write.Positions[0].Reserve.IsSpecified() {
				t.Fatal("blocked policy changed reserve")
			}
		})
	}
}
