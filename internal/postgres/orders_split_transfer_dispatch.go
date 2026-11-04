package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func enrichOrderSplitTransferBusy(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, numbers []string) error {
	if len(numbers) == 0 {
		return nil
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT child.work#>>'{command,split_transfer,parent_posting,posting_number}'
 FROM orders_target_fences f JOIN orders_work child ON child.id=f.work_id
 WHERE f.tenant_id=$1 AND f.moysklad_account=$2 AND f.ozon_account=$3 AND child.dispatch_count=1
 AND child.work#>>'{command,split_transfer,parent_posting,posting_number}'=ANY($4::text[]) LIMIT 10001`, append(ordersScopeArgs(result.Config.Scope), numbers)...)
	if err != nil {
		return ordersError("read active split transfer groups", err)
	}
	defer rows.Close()
	result.SplitTransferBusy = make(map[string]bool)
	for rows.Next() {
		var number string
		if err = rows.Scan(&number); err != nil {
			return ordersError("decode active split transfer group", err)
		}
		result.SplitTransferBusy[number] = true
	}
	return ordersRowsError("read active split transfer groups", rows.Err())
}

// Called under the same account-scope transaction lock as every dispatch Begin.
// A database fence cannot revoke a request already sent to an external system.
func validateOrderSplitTransferDispatch(ctx context.Context, tx pgx.Tx, work orders.Work, guard orders.DispatchGuard, now time.Time) error {
	var childActive bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_target_fences f JOIN orders_work child ON child.id=f.work_id
 WHERE f.tenant_id=$1 AND f.moysklad_account=$2 AND f.ozon_account=$3 AND child.dispatch_count=1
 AND child.work#>>'{command,split_transfer,parent_posting,posting_number}'=$4)`, append(ordersScopeArgs(work.Scope), work.Command.PostingNumber)...).Scan(&childActive)
	if err != nil {
		return ordersError("check split reserve parent dispatch fence", err)
	}
	if childActive {
		return orders.ErrSplitTransferBusy
	}
	proof := work.Command.SplitTransfer
	if proof == nil || !guard.Begin {
		return nil
	}
	if guard.SplitParentSource == nil || guard.SplitParentTarget == nil {
		return orders.ErrBlocked
	}
	source, target := guard.SplitParentSource, guard.SplitParentTarget
	checked := source.Evidence.CheckedAt
	if checked.IsZero() {
		checked = source.Evidence.ObservedAt
	}
	if !freshSplitParentCheck(checked, now) || !freshSplitParentCheck(target.ObservedAt, now) || !proof.ParentSourceMatches(*source) || !proof.ParentTargetMatches(*target) {
		return orders.ErrStale
	}
	return nil
}

func freshSplitParentCheck(checked, now time.Time) bool {
	return !checked.IsZero() && !checked.Before(now.Add(-30*time.Second)) && !checked.After(now.Add(5*time.Second))
}
