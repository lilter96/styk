package orders

import "testing"

func TestNativeTaxRequiresObservedIncludedPercentage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, rate               string
		line, document, included Field[bool]
		want                     bool
	}{
		{"included", "20", Present(true), Present(true), Present(true), true},
		{"normalized integer", "22.00", Present(true), Present(true), Present(true), true},
		{"zero exempt", "0", Present(false), Field[bool]{}, Field[bool]{}, true},
		{"zero taxed", "0", Present(true), Present(true), Present(false), true},
		{"Ozon fraction not native percent", "0.2", Present(true), Present(true), Present(true), false},
		{"excluded", "20", Present(true), Present(true), Present(false), false},
		{"unknown inclusion", "20", Present(true), Present(true), Field[bool]{}, false},
		{"disabled document", "20", Present(true), Present(false), Present(true), false},
		{"disabled line", "20", Present(false), Present(true), Present(true), false},
		{"unknown line", "20", Field[bool]{}, Present(true), Present(true), false},
		{"negative", "-20", Present(true), Present(true), Present(true), false},
		{"overflow", "999999999999999999999999999", Present(true), Present(true), Present(true), false},
		{"over percentage", "101", Present(true), Present(true), Present(true), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := NativeMovementLine{VAT: Present(Decimal(tc.rate)), VATEnabled: tc.line}
			if got := NativeTaxPreservesPrice(line, tc.document, tc.included); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNativeIncludedVATStillRequiresExactOwnedMoney(t *testing.T) {
	t.Parallel()
	command, _ := nativeDomainFixture(t)
	movement := command.NativeMovement
	movement.VATEnabled, movement.VATIncluded = Present(true), Present(true)
	movement.Lines[0].VAT, movement.Lines[0].VATEnabled = Present(Decimal("20")), Present(true)
	command.TargetPositions[0].VAT, command.TargetPositions[0].VATEnabled = movement.Lines[0].VAT, Present(true)
	if err := movement.Validate(command); err != nil {
		t.Fatal(err)
	}
	command.TargetPositions[0].VAT = Present(Decimal("22"))
	if movement.Validate(command) == nil {
		t.Fatal("changed native tax accepted after review")
	}
}
