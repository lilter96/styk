package ozon

import (
	"context"
	"errors"
	"fmt"

	"github.com/lilter96/styk/internal/orders"
)

const financeRealizationPath = "/v2/finance/realization"
const realizationContractVersion = "ozon-realization-v2-2026-11-06"
const maxRealizationRows = 10000 // Local preview bound, not a provider limit.

var _ orders.RealizationReader = (*OrdersClient)(nil)

// ReadRealizationReport uses the existing authenticated, bounded read transport.
// Reports are read once, never manufactured from posting status or accruals.
func (c *OrdersClient) ReadRealizationReport(ctx context.Context, scope orders.Scope, period orders.RealizationPeriod) (orders.RealizationReport, error) {
	if scope != c.scope || scope.Validate() != nil {
		return orders.RealizationReport{}, orders.ErrInvalid
	}
	if err := period.Validate(c.now()); err != nil {
		return orders.RealizationReport{}, err
	}
	data, err := c.orderPost(ctx, financeRealizationPath, period)
	if err != nil {
		return orders.RealizationReport{}, fmt.Errorf("read Ozon monthly realization: %w", realizationReadError(err))
	}
	report, err := decodeRealization(data, scope, period, c.now())
	if err != nil {
		return orders.RealizationReport{}, err
	}
	if c.options.FinanceEvidenceSealer != nil {
		record, err := c.options.FinanceEvidenceSealer.SealFinancialEvidence(ctx, scope, period, c.options.FinanceCredentialRevision, data, report.Fingerprint)
		if err != nil {
			return orders.RealizationReport{}, fmt.Errorf("protect authoritative financial evidence: %w", err)
		}
		report.Evidence = &record
	}
	return report, nil
}

func realizationReadError(err error) error {
	var api *APIError
	if !errors.As(err, &api) {
		return err
	}
	code := "REALIZATION_PROVIDER_UNAVAILABLE"
	switch api.Status {
	case 401, 403:
		code = "REALIZATION_PERMISSION_REQUIRED"
	case 404:
		code = "REALIZATION_SOURCE_NOT_FOUND"
	case 429:
		code = "REALIZATION_RATE_LIMITED"
	}
	return &orders.RealizationReadError{Code: code, Cause: err}
}
