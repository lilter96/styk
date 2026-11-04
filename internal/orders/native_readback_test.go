package orders

import (
	"testing"
	"time"
)

func TestNativeParentReadbackPreservesForeignReserve(t *testing.T) {
	t.Parallel()
	command, _ := nativeDomainFixture(t)
	movement := command.NativeMovement
	native := movement.SourceBaseline.Bindings[0].Native
	native.Reserve = Present(Decimal("2"))
	native.Shipped = Present(Decimal("0"))
	foreign := native
	foreign.ID = "00000000-0000-4000-8000-000000000099"
	foreign.Reserve = Present(Decimal("1"))
	command.TargetPositions = []TargetPosition{native, foreign}
	command.TargetPositionsComplete = true
	target := TargetObservation{Fields: CustomerOrderWrite{OrganizationID: movement.OrganizationID, AgentID: movement.AgentID, StoreID: movement.StoreID, CurrencyID: movement.CurrencyID}, Scope: command.Scope, Correlation: command.Correlation, Complete: true, Positions: []TargetPosition{native, foreign}, ObservedAt: movement.Moment}
	if !NativeParentReadback(command, target) {
		t.Fatal("unchanged draft failed")
	}
	target.Positions[0].Reserve = Present(Decimal("0"))
	if NativeParentReadback(command, target) {
		t.Fatal("draft allowed stock side effect")
	}
	movement.Applicable = Present(true)
	target.Positions[0].Shipped = Present(Decimal("2"))
	if !NativeParentReadback(command, target) {
		t.Fatal("native selected-line effect rejected")
	}
	target.Positions[1].Reserve = Present(Decimal("0"))
	if NativeParentReadback(command, target) {
		t.Fatal("foreign reserve release accepted")
	}
}

func TestNativeReadbackCannotClaimDifferentDocument(t *testing.T) {
	t.Parallel()
	command, _ := nativeDomainFixture(t)
	movement := command.NativeMovement
	actual := NativeDocumentObservation{Scope: command.Scope, Correlation: movement.Correlation, OrganizationID: movement.OrganizationID, AgentID: movement.AgentID, StoreID: movement.StoreID, CurrencyID: movement.CurrencyID, RateValue: movement.RateValue, CustomerOrderID: Present(command.Correlation.CustomerOrderID), Applicable: movement.Applicable, VATEnabled: movement.VATEnabled, VATIncluded: movement.VATIncluded, Moment: Present(movement.Moment), Sum: Present(Money{Amount: "20", Currency: movement.Lines[0].Price.Currency}), Revision: "revision", ObservedAt: movement.Moment, Complete: true}
	actual.Correlation.DocumentID = "00000000-0000-4000-8000-000000000098"
	line := movement.Lines[0]
	actual.Positions = []NativeDocumentPosition{{ID: "00000000-0000-4000-8000-000000000097", ItemKind: line.Mapping.MoyskladKind, ItemID: line.Mapping.MoyskladID, Quantity: line.Quantity, Price: line.Price, Discount: line.Discount, VAT: line.VAT, VATEnabled: line.VATEnabled}}
	if !NativeDocumentMatches(command, actual) {
		t.Fatal("exact native facts rejected")
	}
	actual.Moment = Present(movement.Moment.Add(time.Second))
	if NativeDocumentMatches(command, actual) {
		t.Fatal("within-minute different physical document moment accepted")
	}
	actual.Moment = Present(movement.Moment.Add(999 * time.Microsecond))
	if !NativeDocumentMatches(command, actual) {
		t.Fatal("provider millisecond precision rejected")
	}
	actual.Moment = Present(movement.Moment)
	actual.CustomerOrderID = Present("00000000-0000-4000-8000-000000000096")
	if NativeDocumentMatches(command, actual) {
		t.Fatal("different native parent accepted")
	}
}
