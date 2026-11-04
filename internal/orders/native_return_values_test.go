package orders

import "testing"

func nativeTaxReturnFixture(t *testing.T) NativeMovement {
	t.Helper()
	command, _ := nativeDomainFixture(t)
	movement := *command.NativeMovement
	movement.DemandWorkID = "known-demand"
	movement.VATEnabled, movement.VATIncluded = Present(true), Present(true)
	line := &movement.Lines[0]
	line.BasisPositionID = "00000000-0000-4000-8000-000000000098"
	line.VAT, line.VATEnabled = Present(Decimal("20")), Present(true)
	basis := &NativeDocumentObservation{Scope: command.Scope, Correlation: movement.Correlation, OrganizationID: movement.OrganizationID, AgentID: movement.AgentID, StoreID: movement.StoreID, CurrencyID: movement.CurrencyID, CustomerOrderID: Present(command.Correlation.CustomerOrderID), Applicable: Present(true), VATEnabled: movement.VATEnabled, VATIncluded: movement.VATIncluded, Moment: Present(movement.Moment), Sum: Present(line.Price), Revision: "basis", ObservedAt: movement.Moment, Complete: true, ReturnsComplete: true, ReturnsStartedAt: movement.Moment, ReturnsFinishedAt: movement.Moment}
	basis.Correlation.DocumentID = "00000000-0000-4000-8000-000000000099"
	basis.Positions = []NativeDocumentPosition{{ID: line.BasisPositionID, ItemKind: line.Mapping.MoyskladKind, ItemID: line.Mapping.MoyskladID, Quantity: line.Quantity, Price: line.Price, Discount: line.Discount, VAT: line.VAT, VATEnabled: line.VATEnabled}}
	movement.DemandBasis = basis
	return movement
}

func TestNativeReturnRequiresFrozenBasisFiscalValues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*NativeMovement)
	}{
		{"price", func(m *NativeMovement) { m.Lines[0].Price.Amount = "999" }},
		{"currency", func(m *NativeMovement) { m.Lines[0].Price.Currency = "USD" }},
		{"rate", func(m *NativeMovement) { m.Lines[0].VAT = Present(Decimal("22")) }},
		{"discount", func(m *NativeMovement) { m.Lines[0].Discount = Present(Decimal("10")) }},
		{"enabled", func(m *NativeMovement) { m.Lines[0].VATEnabled = Present(false) }},
		{"basis id", func(m *NativeMovement) { m.Lines[0].BasisPositionID = "00000000-0000-4000-8000-000000000096" }},
		{"duplicate basis", func(m *NativeMovement) { m.Lines = append(m.Lines, m.Lines[0]) }},
		{"header", func(m *NativeMovement) { m.VATIncluded = Present(false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			movement := nativeTaxReturnFixture(t)
			if err := movement.validateBasis(ActionSalesReturnCreate); err != nil {
				t.Fatal(err)
			}
			tc.change(&movement)
			if movement.validateBasis(ActionSalesReturnCreate) == nil {
				t.Fatal("return values not bound to original demand")
			}
		})
	}
}
