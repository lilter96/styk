package orderflow

import (
	"github.com/lilter96/styk/internal/orders"
	"testing"
	"time"
)

func nativeReturnBasisFixture() (orders.NativeMovementLine, orders.NativeDocumentObservation) {
	id := "00000000-0000-4000-8000-000000000001"
	row := orders.NativeDocumentPosition{ID: id, ItemKind: "product", ItemID: id, Quantity: "10", Price: orders.Money{Amount: "12.34", Currency: "RUB"}, Discount: orders.Present(orders.Decimal("0")), VAT: orders.Present(orders.Decimal("0")), VATEnabled: orders.Present(false)}
	now := time.Now().UTC()
	basis := orders.NativeDocumentObservation{Scope: orders.Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}, Correlation: orders.NativeDocumentCorrelation{Kind: "demand", SyncID: id, DocumentID: id, Marker: "basis"}, OrganizationID: id, AgentID: id, StoreID: id, CurrencyID: id, Applicable: orders.Present(true), VATEnabled: orders.Present(false), VATIncluded: orders.Present(false), Moment: orders.Present(now), Sum: orders.Present(orders.Money{Amount: "123.4", Currency: "RUB"}), Positions: []orders.NativeDocumentPosition{row}, ReturnsComplete: true, ReturnsStartedAt: now, ReturnsFinishedAt: now, ObservedAt: now, Revision: "basis-revision", Complete: true}
	line := orders.NativeMovementLine{Mapping: orders.ProductMappingRef{MoyskladKind: "product", MoyskladID: id}, Quantity: "3", Price: orders.Money{Amount: "99", Currency: "RUB"}}
	return line, basis
}

func TestNativeReturnUsesBasisMoneyAndAllPostedReturns(t *testing.T) {
	t.Parallel()
	line, basis := nativeReturnBasisFixture()
	returned := basis.Positions[0]
	returned.Quantity = "7"
	basis.Returns = []orders.NativeReturnEvidence{{ID: "00000000-0000-4000-8000-000000000002", DemandID: basis.Correlation.DocumentID, Applicable: orders.Present(true), Positions: []orders.NativeDocumentPosition{returned}, Revision: "prior-return", ObservedAt: basis.ObservedAt}}
	result, err := planNativeReturnLine(line, basis)
	if err != nil || result.Price != basis.Positions[0].Price || result.BasisPositionID != basis.Positions[0].ID {
		t.Fatalf("original basis money lost: %+v %v", result, err)
	}
	line.Quantity = "3.000000000000000001"
	if _, err = planNativeReturnLine(line, basis); err == nil {
		t.Fatal("aggregate returns exceed exact original quantity")
	}
	basis.Returns[0].Applicable = orders.Present(false)
	if _, err = planNativeReturnLine(line, basis); err != nil {
		t.Fatal("draft return incorrectly consumed physical return allowance", err)
	}
	basis.ReturnsComplete = false
	if _, err = planNativeReturnLine(line, basis); err == nil {
		t.Fatal("partial return traversal became zero consumed quantity")
	}
}
