package ordermoney

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestNativeReturnRechecksExactRemainingQuantityAtPayloadBoundary(t *testing.T) {
	t.Parallel()
	for _, posted := range []bool{true, false} {
		line, basis := nativeReturnMoneyFixture()
		prior := basis.Positions[0]
		prior.Quantity = "7"
		basis.Returns = []orders.NativeReturnEvidence{{ID: "00000000-0000-4000-8000-000000000003", DemandID: basis.Correlation.DocumentID, Applicable: orders.Present(posted), Positions: []orders.NativeDocumentPosition{prior}, Revision: "returned", ObservedAt: basis.ObservedAt}}
		movement := &orders.NativeMovement{DemandBasis: &basis, Lines: []orders.NativeMovementLine{line}}
		if got := nativeReturnAmounts(movement); got.Code != "" {
			t.Fatal(got)
		}
		movement.Lines[0].Quantity = "3.000000000000000001"
		if got := nativeReturnAmounts(movement); (got.Code == "native_return_quantity_exceeded") != posted {
			t.Fatal("return or draft consumed incorrectly", got)
		}
		movement.Lines = append(movement.Lines, movement.Lines[0])
		if got := nativeReturnAmounts(movement); got.Code == "" {
			t.Fatal("duplicate basis position accepted")
		}
		basis.ReturnsComplete = false
		if got := nativeReturnAmounts(movement); got.Code != "native_return_basis_unknown" {
			t.Fatal("partial coverage became complete", got)
		}
	}
}

func nativeReturnMoneyFixture() (orders.NativeMovementLine, orders.NativeDocumentObservation) {
	id := "00000000-0000-4000-8000-000000000001"
	now := time.Now().UTC()
	line := orders.NativeMovementLine{BasisPositionID: id, Quantity: "3", Mapping: orders.ProductMappingRef{MoyskladKind: "product", MoyskladID: id}}
	position := orders.NativeDocumentPosition{ID: id, ItemKind: "product", ItemID: id, Quantity: "10", Price: orders.Money{Amount: "12.34", Currency: "RUB"}, Discount: orders.Present(orders.Decimal("0")), VAT: orders.Present(orders.Decimal("20")), VATEnabled: orders.Present(true)}
	basis := orders.NativeDocumentObservation{Scope: orders.Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}, Correlation: orders.NativeDocumentCorrelation{Kind: "demand", SyncID: id, DocumentID: id, Marker: "basis"}, OrganizationID: id, AgentID: id, StoreID: id, CurrencyID: id, Applicable: orders.Present(true), VATEnabled: orders.Present(true), VATIncluded: orders.Present(true), Moment: orders.Present(now), Sum: orders.Present(orders.Money{Amount: "123.4", Currency: "RUB"}), Positions: []orders.NativeDocumentPosition{position}, ReturnsComplete: true, ReturnsStartedAt: now, ReturnsFinishedAt: now, ObservedAt: now, Revision: "basis", Complete: true}
	return line, basis
}
