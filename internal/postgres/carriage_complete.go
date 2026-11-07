package postgres

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) CompleteCarriageWork(ctx context.Context, result orders.CarriageCompletion) error {
	if result.Scope.Validate() != nil || result.WorkID == "" || result.ClaimToken == "" {
		return orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ordersError("begin carriage completion", err)
	}
	defer rollbackMerchantMutation(tx)
	work, err := lockCarriageWork(ctx, tx, result.Scope, result.WorkID, result.ClaimToken)
	if err != nil {
		return err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	if err = applyCarriageCompletion(&work, result, now); err != nil {
		return err
	}
	if err = writeCarriageWork(ctx, tx, &work, now); err != nil {
		return err
	}
	if err = journalCarriageAttempt(ctx, tx, work); err != nil {
		return err
	}
	if err = finishCarriageTask(ctx, tx, work); err != nil {
		return err
	}
	return ordersRowsError("commit carriage completion", tx.Commit(ctx))
}

func applyCarriageCompletion(work *orders.CarriageWork, result orders.CarriageCompletion, now time.Time) error {
	if work.State != result.ExpectedState || !carriageCompletionAllowed(*work, result.State) {
		return orders.ErrStale
	}
	if result.CarriageID != "" {
		if !orders.CarriageIDValid(result.CarriageID) || work.CarriageID != "" && work.CarriageID != result.CarriageID {
			return orders.ErrStale
		}
		if work.CarriageID == "" && (result.Receipt.State != orders.StateAcknowledged || result.Receipt.ExternalID != result.CarriageID) {
			return orders.ErrBlocked
		}
		work.CarriageID = result.CarriageID
	}
	if result.State == orders.StateReconciled {
		if result.Observation == nil || !work.OutcomeMatches(*result.Observation) || result.Observation.ObservedAt.Before(work.CreatedAt) || result.Observation.ObservedAt.After(now.Add(time.Minute)) {
			return orders.ErrBlocked
		}
	}
	if result.Receipt.CommandID != "" {
		if result.Receipt.CommandID != work.Plan.ID || result.Receipt.ObservedAt.After(now.Add(time.Minute)) {
			return orders.ErrInvalid
		}
		if work.Receipt.State != orders.StateUnknown {
			work.Receipt = result.Receipt
		}
	}
	work.State, work.Code, work.Observation = result.State, ordersSafeCode(result.Code), result.Observation
	work.NextRunAt = result.NextRunAt
	if work.NextRunAt.Before(now) {
		work.NextRunAt = now
	}
	if work.NextRunAt.After(now.Add(24 * time.Hour)) {
		return orders.ErrInvalid
	}
	return nil
}

func carriageCompletionAllowed(work orders.CarriageWork, next orders.State) bool {
	if work.DispatchCount == 0 {
		return work.State == orders.StateClaimed && (next == orders.StateBlocked || next == orders.StateRejected)
	}
	if work.DispatchCount != 1 {
		return false
	}
	if work.State == orders.StateDispatching {
		return next.ReadOnly()
	}
	return work.State.ReadOnly() && (next.ReadOnly() || next == orders.StateReconciled)
}
