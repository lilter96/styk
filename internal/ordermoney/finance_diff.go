package ordermoney

import "github.com/lilter96/styk/internal/orders"

func FinancialSourceDiff(before, after orders.RealizationReport) (*orders.CommissionSourceDiff, error) {
	diff, err := orders.DiffCommissionSource(before, after)
	if err != nil {
		return nil, err
	}
	left, err := ClassifyRealization(before)
	if err != nil {
		return nil, err
	}
	right, err := ClassifyRealization(after)
	if err != nil {
		return nil, err
	}
	diff.ComponentChanges, err = orders.DiffFinancialLedgers(left, right)
	if err != nil {
		return nil, err
	}
	return diff, nil
}
