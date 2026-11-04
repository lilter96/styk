package ordermoney

import (
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestNativeIncludedVATPreservesGrossTotalWithoutTaxRounding(t *testing.T) {
	t.Parallel()
	line := orders.NativeMovementLine{Quantity: "3", Price: orders.Money{Amount: "0.33", Currency: "RUB"}, Discount: orders.Present(orders.Decimal("0")), VAT: orders.Present(orders.Decimal("22")), VATEnabled: orders.Present(true)}
	movement := &orders.NativeMovement{VATEnabled: orders.Present(true), VATIncluded: orders.Present(true), Lines: []orders.NativeMovementLine{line}}
	command := orders.Command{NativeMovement: movement}
	actual := orders.NativeDocumentObservation{Sum: orders.Present(orders.Money{Amount: "0.99", Currency: "RUB"})}
	if got := NativeDocumentTotal(command, actual); got.Code != "" {
		t.Fatal(got)
	}
	actual.Sum.Value.Amount = "1.21"
	if got := NativeDocumentTotal(command, actual); got.Code != "order_total_mismatch" {
		t.Fatal("tax added again or mismatch hidden", got)
	}
	movement.VATIncluded = orders.Present(false)
	if got := NativeDocumentAmounts(movement); got.Code != "native_tax_or_discount_rounding_unverified" {
		t.Fatal("excluded tax silently treated as included", got)
	}
	movement.VATIncluded = orders.Present(true)
	movement.Lines[0].Price.Amount = "0.335"
	if got := NativeDocumentAmounts(movement); got.Code != "native_sum_rounding_unverified" {
		t.Fatal("included tax bypassed monetary precision", got)
	}
}
