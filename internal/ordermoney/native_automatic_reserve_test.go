package ordermoney

import (
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestAutomaticNativeDemandRequiresObservedExactReserveRelease(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, before, after, quantity string
		allow                         bool
	}{
		{name: "reserved full shipment", before: "1", after: "0", quantity: "1", allow: true},
		{name: "zero reserve remains zero", before: "0", after: "0", quantity: "1", allow: true},
		{name: "partial reserved quantity", before: "0.5", after: "0", quantity: "1", allow: true},
		{name: "partial shipment retains remaining reserve", before: "2", after: "1", quantity: "1", allow: true},
		{name: "reserve not released", before: "1", after: "1", quantity: "1"},
		{name: "phantom reserve release", before: "2", after: "0", quantity: "1"},
		{name: "exact fractional reserve", before: "1.000000000000000001", after: "0.000000000000000001", quantity: "1", allow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			command := orders.Command{Action: orders.ActionDemandCreate, MerchantIntent: &orders.MerchantIntent{}, NativeMovement: &orders.NativeMovement{Applicable: orders.Present(true), Lines: []orders.NativeMovementLine{{OrderPositionID: "line", Quantity: orders.Decimal(tc.quantity)}}}, TargetPositions: []orders.TargetPosition{{ID: "line", Shipped: orders.Present(orders.Decimal("0")), Reserve: orders.Present(orders.Decimal(tc.before))}}}
			target := orders.TargetObservation{Positions: []orders.TargetPosition{{ID: "line", Shipped: orders.Present(orders.Decimal(tc.quantity)), Reserve: orders.Present(orders.Decimal(tc.after))}}}
			if got := NativeParentEffectObserved(command, target); got != tc.allow {
				t.Fatalf("native reserve readback=%v want %v", got, tc.allow)
			}
		})
	}
}
