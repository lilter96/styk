package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

// ObserveCommissionReport retains source revisions independently of immutable
// native execution results. A correction never mutates or reenqueues that work.
func (s *Store) ObserveCommissionReport(ctx context.Context, source orders.RealizationReport) error {
	if source.Scope.Validate() != nil || len(source.Fingerprint) != 64 {
		return orders.ErrInvalid
	}
	fingerprint, err := ordermoney.CommissionSourceFingerprint(source)
	if err != nil || fingerprint != source.Fingerprint {
		return orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ordersError("begin authoritative commission observation", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockOrderReviewScope(ctx, tx, source.Scope); err != nil {
		return err
	}
	config, err := readOrderConfig(ctx, tx, source.Scope)
	if err != nil {
		return err
	}
	if err = validateOrderReadAuthority(ctx, tx, config); err != nil {
		return err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	if source.Period.Validate(now) != nil || source.ObservedAt.IsZero() || source.ObservedAt.After(now) {
		return orders.ErrStale
	}
	if err = observeCommissionSource(ctx, tx, source); err != nil {
		return err
	}
	return ordersRowsError("commit authoritative commission observation", tx.Commit(ctx))
}
