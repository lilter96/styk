package ordermoney

import (
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestFinancialSourceDiffNamesExactSignedComponentAmendments(t *testing.T) {
	t.Parallel()
	_, before, _, _, _ := commissionFixture(t)
	var err error
	before.Fingerprint, err = CommissionSourceFingerprint(before)
	if err != nil {
		t.Fatal(err)
	}
	after := before
	after.Rows = append([]orders.RealizationRow{}, before.Rows...)
	after.Rows[0].Delivery.Bonus = orders.Present(orders.Decimal("25"))
	after.Rows[0].Delivery.Total = orders.Present(orders.Decimal("205"))
	after.Fingerprint, err = CommissionSourceFingerprint(after)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := FinancialSourceDiff(before, after)
	if err != nil || len(diff.ComponentChanges) != 2 || len(diff.ChangedRows) != 1 {
		t.Fatal("semantic correction components lost", diff, err)
	}
	if diff.ComponentChanges[0].Component != "bonus" || *diff.ComponentChanges[0].Before.Value != "5" || *diff.ComponentChanges[0].After.Value != "25" {
		t.Fatal("wrong exact amendment", diff.ComponentChanges)
	}
	if before.Rows[0].Delivery.Bonus.Value == after.Rows[0].Delivery.Bonus.Value {
		t.Fatal("source evidence was mutated")
	}
}
