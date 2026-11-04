package ordermoney

import (
	"github.com/lilter96/styk/internal/orders"
	"testing"
)

func TestNativeSumMismatchNeverBecomesReconciled(t *testing.T) {
	t.Parallel()
	line := orders.NativeMovementLine{Quantity: "3", Price: orders.Money{Amount: "0.335", Currency: "RUB"}, Discount: orders.Present(orders.Decimal("0")), VAT: orders.Present(orders.Decimal("0")), VATEnabled: orders.Present(false)}
	command := orders.Command{NativeMovement: &orders.NativeMovement{Lines: []orders.NativeMovementLine{line}}}
	actual := orders.NativeDocumentObservation{Sum: orders.Present(orders.Money{Amount: "1.01", Currency: "RUB"})}
	if result := NativeDocumentTotal(command, actual); result.Code != "" {
		t.Fatal(result)
	}
	actual.Sum = orders.Present(orders.Money{Amount: "1", Currency: "RUB"})
	if result := NativeDocumentTotal(command, actual); result.Code != "order_total_mismatch" {
		t.Fatal(result)
	}
	command.NativeMovement.Lines[0].VATEnabled = orders.Present(true)
	command.NativeMovement.Lines[0].VAT = orders.Present(orders.Decimal("20"))
	if result := NativeDocumentTotal(command, actual); result.Code != "native_tax_or_discount_rounding_unverified" {
		t.Fatal(result)
	}
}

func TestNativePostedDemandWaitsForExactParentShipment(t *testing.T) {
	t.Parallel()
	command := orders.Command{Action: orders.ActionDemandCreate, NativeMovement: &orders.NativeMovement{Applicable: orders.Present(true), Lines: []orders.NativeMovementLine{{OrderPositionID: "native-line", Quantity: "1.000000000000000001"}}}, TargetPositions: []orders.TargetPosition{{ID: "native-line", Shipped: orders.Present(orders.Decimal("2"))}}}
	target := orders.TargetObservation{Positions: []orders.TargetPosition{{ID: "native-line", Shipped: orders.Present(orders.Decimal("3.000000000000000001"))}}}
	if !NativeParentEffectObserved(command, target) {
		t.Fatal("exact native effect lost decimal precision")
	}
	target.Positions[0].Shipped = orders.Present(orders.Decimal("2"))
	if NativeParentEffectObserved(command, target) {
		t.Fatal("stale native parent was confirmed")
	}
	target.Positions[0].Shipped = orders.Present(orders.Decimal("4"))
	if NativeParentEffectObserved(command, target) {
		t.Fatal("contradictory extra movement hidden")
	}
}
