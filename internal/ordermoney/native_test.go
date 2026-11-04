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
