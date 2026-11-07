package orderflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) authorizeDispatch(ctx context.Context, work orders.Work, changedCode string) (bool, error) {
	allowed, err := r.store.GuardOrderDispatch(ctx, orders.DispatchGuard{Work: work, Now: r.now()})
	if errors.Is(err, orders.ErrSplitTransferBusy) {
		return false, r.stop(ctx, work, "SPLIT_RESERVE_GROUP_BUSY")
	}
	if errors.Is(err, orders.ErrBlocked) || errors.Is(err, orders.ErrStale) {
		return false, r.stop(ctx, work, changedCode)
	}
	if err != nil {
		return false, fmt.Errorf("check order dispatch authority: %w", err)
	}
	if !allowed {
		return false, r.stop(ctx, work, changedCode)
	}
	return true, nil
}
