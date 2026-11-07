package orders

import (
	"testing"
	"time"
)

func carriageOutcomeFixture() (CarriageWork, CarriageSnapshot) {
	now := time.Now().UTC()
	scope := Scope{Tenant: "tenant", OzonAccount: "10", MoyskladAccount: "ms"}
	member := CarriageMember{PostingNumber: "p1", Scheme: "FBS", WarehouseID: "30", DeliveryMethodID: "20", Status: "awaiting_deliver", Ready: true, SourceFingerprint: "before", Lines: []PostingLine{{Identity: LineIdentity{SKU: "100", OfferID: "offer"}, Quantity: "1"}}}
	before := CarriageSnapshot{Scope: scope, DeliveryMethodID: "20", WarehouseID: "30", CarriageID: "40", Complete: true, Members: []CarriageMember{member}, Header: &CarriageHeader{CarriageID: "40", CompanyID: "10", DeliveryMethodID: "20", WarehouseID: "30", Status: "new", UpdatedAt: now}}
	plan := CarriagePlan{Scope: scope, CarriageInput: CarriageInput{Action: CarriageApprove, CarriageID: "40", DeliveryMethodID: "20"}, WarehouseID: "30", Members: []CarriageMember{member}, Snapshot: before}
	actual := before
	header := *before.Header
	header.Status = "closed"
	header.UpdatedAt = now.Add(time.Second)
	header.AvailableActions = []string{"get_waybill"}
	actual.Header = &header
	actual.Documents = &CarriageDocuments{Status: "ready", ActType: "ozon_digital", Added: []string{"p1"}, Removed: []string{}}
	actual.Members = append([]CarriageMember{}, before.Members...)
	actual.Members[0].SourceFingerprint = "after"
	actual.Members[0].Ready = false
	return CarriageWork{Scope: scope, Plan: plan, CarriageID: "40", DispatchCount: 1}, actual
}

func TestCarriageApproveRequiresIndependentDocumentReadinessAndExactComposition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*CarriageWork, *CarriageSnapshot)
		want   bool
	}{
		{"confirmed stable composition despite changed posting observation", func(*CarriageWork, *CarriageSnapshot) {}, true},
		{"no dispatch", func(w *CarriageWork, _ *CarriageSnapshot) { w.DispatchCount = 0 }, false},
		{"foreign company", func(_ *CarriageWork, a *CarriageSnapshot) { a.Header.CompanyID = "11" }, false},
		{"wrong header method", func(_ *CarriageWork, a *CarriageSnapshot) { a.Header.DeliveryMethodID = "21" }, false},
		{"container count not confirmed", func(w *CarriageWork, _ *CarriageSnapshot) { n := int32(3); w.Plan.ContainersCount = &n }, false},
		{"still new", func(_ *CarriageWork, a *CarriageSnapshot) { a.Header.Status = "new" }, false},
		{"cancelled", func(_ *CarriageWork, a *CarriageSnapshot) { a.Header.Status = "cancelled" }, false},
		{"stale header", func(w *CarriageWork, a *CarriageSnapshot) { a.Header.UpdatedAt = w.Plan.Snapshot.Header.UpdatedAt }, false},
		{"no document read", func(_ *CarriageWork, a *CarriageSnapshot) { a.Documents = nil }, false},
		{"document preparing", func(_ *CarriageWork, a *CarriageSnapshot) { a.Documents.Status = "forming" }, false},
		{"partial", func(_ *CarriageWork, a *CarriageSnapshot) { a.Documents.Partial = true }, false},
		{"removed member", func(_ *CarriageWork, a *CarriageSnapshot) { a.Documents.Removed = []string{"p1"} }, false},
		{"foreign document member", func(_ *CarriageWork, a *CarriageSnapshot) { a.Documents.Added = []string{"foreign"} }, false},
		{"quantity changed", func(_ *CarriageWork, a *CarriageSnapshot) {
			a.Members[0].Lines = []PostingLine{{Identity: LineIdentity{SKU: "100", OfferID: "offer"}, Quantity: "2"}}
		}, false},
		{"no document capability", func(_ *CarriageWork, a *CarriageSnapshot) { a.Header.AvailableActions = []string{} }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, a := carriageOutcomeFixture()
			tc.change(&w, &a)
			if got := w.OutcomeMatches(a); got != tc.want {
				t.Fatalf("match=%v want %v", got, tc.want)
			}
		})
	}
}
