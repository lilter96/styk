package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) CompleteCommissionWork(ctx context.Context, result orders.CommissionCompletion) error {
	if result.Scope.Validate() != nil || result.WorkID == "" || result.ClaimToken == "" {
		return orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ordersError("begin commission completion", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockOrderReviewScope(ctx, tx, result.Scope); err != nil {
		return err
	}
	work, err := lockCommissionWork(ctx, tx, result.Scope, result.WorkID, result.ClaimToken)
	if err != nil {
		return err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	if err = applyCommissionCompletion(&work, result, now); err != nil {
		return err
	}
	if err = writeCommissionWork(ctx, tx, &work, now); err != nil {
		return err
	}
	if err = recordCommissionCorrection(ctx, tx, work); err != nil {
		return err
	}
	if err = insertCommissionAttempt(ctx, tx, work, result.Observation); err != nil {
		return err
	}
	if err = finishCommissionTask(ctx, tx, work); err != nil {
		return err
	}
	return ordersRowsError("commit commission completion", tx.Commit(ctx))
}

func applyCommissionCompletion(work *orders.CommissionWork, result orders.CommissionCompletion, now time.Time) error {
	if work.State != result.ExpectedState || !commissionCompletionAllowed(*work, result.State) {
		return orders.ErrStale
	}
	if result.Code == "FINANCE_DOCUMENT_POSTED_EXTERNALLY" {
		if result.State != orders.StateUnknown || result.Observation == nil || !ordermoney.CommissionPostedExternally(work.Plan, *result.Observation, work.Receipt.ExternalID) || result.Observation.ObservedAt.Before(work.CreatedAt) || result.Observation.ObservedAt.After(now.Add(time.Minute)) {
			return orders.ErrBlocked
		}
		work.DocumentID = result.Observation.DocumentID
	}
	if result.State == orders.StateReconciled {
		if result.Observation == nil || !ordermoney.CommissionDraftMatches(work.Plan, *result.Observation) || result.Observation.ObservedAt.Before(work.CreatedAt) || result.Observation.ObservedAt.After(now.Add(time.Minute)) {
			return orders.ErrBlocked
		}
		if work.Receipt.ExternalID != "" && work.Receipt.ExternalID != result.Observation.DocumentID {
			return orders.ErrBlocked
		}
		work.DocumentID = result.Observation.DocumentID
	}
	if result.Receipt.CommandID != "" {
		if result.Receipt.CommandID != work.Plan.ID || result.Receipt.ObservedAt.After(now.Add(time.Minute)) {
			return orders.ErrInvalid
		}
		// Preserve the original uncertain result even after independent readback.
		if work.Receipt.State != orders.StateUnknown {
			work.Receipt = result.Receipt
		}
	}
	work.State, work.Code = result.State, ordersSafeCode(result.Code)
	if result.State == orders.StateReconciled && work.Plan.Accounting != nil && !work.Plan.Accounting.Complete && work.Code == "FINANCE_DRAFT_CONFIRMED" {
		work.Code = "FINANCE_DRAFT_CONFIRMED_NEEDS_ACCOUNTING_REVIEW"
	}
	work.SourceChanged = work.SourceChanged || result.SourceChanged
	work.NextRunAt = result.NextRunAt
	if work.NextRunAt.Before(now) {
		work.NextRunAt = now
	}
	if work.NextRunAt.After(now.Add(24 * time.Hour)) {
		return orders.ErrInvalid
	}
	return nil
}

func commissionCompletionAllowed(work orders.CommissionWork, next orders.State) bool {
	if work.DispatchCount == 0 {
		return work.State == orders.StateClaimed && (next == orders.StateBlocked || next == orders.StateRejected)
	}
	if work.DispatchCount != 1 {
		return false
	}
	if work.State == orders.StateDispatching {
		return next.ReadOnly() || next == orders.StateRejected
	}
	if work.State.ReadOnly() {
		return next.ReadOnly() || next == orders.StateReconciled
	}
	return false
}

func recordCommissionCorrection(ctx context.Context, tx pgx.Tx, work orders.CommissionWork) error {
	if !work.SourceChanged {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE orders_finance_periods SET source_changed=true WHERE work_id=$1`, work.ID)
	return ordersRowsError("record commission source correction", err)
}
