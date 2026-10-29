package application

import (
	"context"
	"fmt"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

// StockDecisionStore keeps loading, evaluating and persisting one decision in
// the same database transaction; it never performs network requests.
type StockDecisionStore interface {
	CalculateStock(context.Context, domain.StockCalculationRequest, time.Time) (domain.StockDecision, error)
}

type StockCalculator struct{ Store StockDecisionStore }

func (c StockCalculator) Run(ctx context.Context, request domain.StockCalculationRequest, now time.Time) (domain.StockDecision, error) {
	if c.Store == nil {
		return domain.StockDecision{}, fmt.Errorf("stock decision store is required")
	}
	d, err := c.Store.CalculateStock(ctx, request, now)
	if err != nil {
		return domain.StockDecision{}, fmt.Errorf("calculate stock commitment: %w", err)
	}
	return d, nil
}
