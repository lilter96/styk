package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) CompleteOrderWork(ctx context.Context, completion orders.Completion) error {
	if completion.Scope.Validate() != nil || completion.WorkID == "" || completion.ClaimToken == "" {
		return orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ordersError("begin order completion", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = ordersLock(ctx, tx, completion.Scope); err != nil {
		return err
	}
	work, err := lockOrderWork(ctx, tx, completion.Scope, completion.WorkID, completion.ClaimToken)
	if err != nil {
		return err
	}
	if err = completeOrderLocked(ctx, tx, work, completion); err != nil {
		return err
	}
	return ordersRowsError("commit order completion", tx.Commit(ctx))
}

func completeOrderLocked(ctx context.Context, tx pgx.Tx, work orders.Work, completion orders.Completion) error {
	if completion.Correlation == nil && completion.ObservedTarget != nil {
		completion.Correlation = &completion.ObservedTarget.Correlation
	}
	if work.State != completion.ExpectedState || !orders.CanTransition(work.State, completion.State) {
		return orders.ErrStale
	}
	if err := validateOrderCompletion(work, completion); err != nil {
		return err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	completion.Code = ordersSafeCode(completion.Code)
	completion.Receipt.Code = ordersSafeCode(completion.Receipt.Code)
	if err = saveOrderCompletionEvidence(ctx, tx, work, completion, now); err != nil {
		return err
	}
	work.State, work.Code, work.NextRunAt = completion.State, completion.Code, completion.NextRunAt
	if completion.Receipt.State != "" {
		work.Receipt = completion.Receipt
	}
	work.LeaseUntil = time.Time{}
	if err = writeOrderWork(ctx, tx, work, now); err != nil {
		return err
	}
	if err = insertOrderAttempt(ctx, tx, work, completion.Code, now); err != nil {
		return err
	}
	return finishOrderTask(ctx, tx, work, now)
}

func validateOrderCompletion(work orders.Work, completion orders.Completion) error {
	receipt := completion.Receipt
	if receipt.State != "" {
		if receipt.CommandID != work.Command.ID || len(receipt.TaskID) > 256 || len(receipt.ExternalID) > 256 || (receipt.HTTPStatus != 0 && (receipt.HTTPStatus < 100 || receipt.HTTPStatus > 599)) {
			return orders.ErrInvalid
		}
		if work.Receipt.TaskID != "" && receipt.TaskID != "" && work.Receipt.TaskID != receipt.TaskID {
			return orders.ErrStale
		}
	}
	if completion.Correlation != nil {
		correlation := *completion.Correlation
		if correlation.Validate() != nil || correlation.Scope != work.Scope || correlation.PostingNumber != work.Command.PostingNumber || correlation.SyncID != work.Command.Correlation.SyncID || correlation.Marker != work.Command.Correlation.Marker {
			return orders.ErrStale
		}
		if work.Command.Correlation.CustomerOrderID != "" && correlation.CustomerOrderID != work.Command.Correlation.CustomerOrderID {
			return orders.ErrStale
		}
		if receipt.ExternalID != "" && correlation.CustomerOrderID != receipt.ExternalID {
			return orders.ErrStale
		}
	}
	if completion.ObservedPosting != nil && (completion.ObservedPosting.Validate() != nil || completion.ObservedPosting.Scope != work.Scope || completion.ObservedPosting.PostingNumber != work.Command.PostingNumber || completion.ObservedPosting.CommercialOrderID != work.Command.CommercialOrderID) {
		return orders.ErrStale
	}
	if completion.State == orders.StateReconciled {
		return validateOrderReconciliation(work, completion)
	}
	return nil
}

func validateOrderReconciliation(work orders.Work, completion orders.Completion) error {
	if work.Command.Action.IsMirror() {
		if completion.Correlation == nil || completion.Correlation.CustomerOrderID == "" || completion.ObservedTarget == nil {
			return orders.ErrInvalid
		}
		if !orderReadbackMatches(work.Command, *completion.ObservedTarget) {
			return orders.ErrStale
		}
		return nil
	}
	if completion.ObservedPosting == nil {
		return orders.ErrInvalid
	}
	return nil
}

func saveOrderCompletionEvidence(ctx context.Context, tx pgx.Tx, work orders.Work, completion orders.Completion, now time.Time) error {
	if completion.Receipt.State != "" {
		raw, err := ordersJSON(completion.Receipt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO orders_receipts(work_id,tenant_id,moysklad_account,ozon_account,receipt,created_at) VALUES($1,$2,$3,$4,$5,$6)`, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, raw, now)
		if err != nil {
			return ordersError("save sanitized order receipt", err)
		}
	}
	if completion.State != orders.StateReconciled {
		return nil
	}
	if work.Command.Action.IsMirror() {
		if err := resolveOrderProjection(ctx, tx, *completion.Correlation, now); err != nil {
			return err
		}
	}
	return insertOrderReconciliation(ctx, tx, work, completion, now)
}

func resolveOrderProjection(ctx context.Context, tx pgx.Tx, correlation orders.Correlation, now time.Time) error {
	args := append(ordersScopeArgs(correlation.Scope), correlation.PostingNumber, correlation.SyncID, correlation.Marker, correlation.CustomerOrderID, now)
	tag, err := tx.Exec(ctx, `UPDATE orders_projection_bindings SET customer_order_id=$7,resolved_at=COALESCE(resolved_at,$8) WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=$4 AND sync_id=$5 AND marker=$6 AND (customer_order_id IS NULL OR customer_order_id=$7)`, args...)
	if err != nil {
		return ordersError("resolve exact order projection identity", err)
	}
	if tag.RowsAffected() != 1 {
		return orders.ErrStale
	}
	return nil
}

func insertOrderReconciliation(ctx context.Context, tx pgx.Tx, work orders.Work, completion orders.Completion, now time.Time) error {
	correlation := work.Command.Correlation
	if completion.Correlation != nil {
		correlation = *completion.Correlation
	}
	raw, err := ordersJSON(correlation)
	if err != nil {
		return err
	}
	var posting []byte
	if completion.ObservedPosting != nil {
		posting, err = ordersJSON(completion.ObservedPosting)
		if err != nil {
			return err
		}
	}
	var target []byte
	if completion.ObservedTarget != nil {
		target, err = ordersJSON(sanitizedOrderTarget(work.Command, *completion.ObservedTarget))
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_reconciliations(work_id,tenant_id,moysklad_account,ozon_account,correlation,observed_posting,observed_target,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, raw, posting, target, now)
	return ordersRowsError("save complete order readback evidence", err)
}
