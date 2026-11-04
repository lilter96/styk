package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// One set-based read per selected batch; no provider I/O or network transaction.
// Only immutable successful reconciliation evidence can establish line ownership.
func enrichOrderPositionBaselines(ctx context.Context, q ordersQuerier, result *orders.ReviewContext) error {
	numbers := make([]string, 0, len(result.Postings))
	for _, posting := range result.Postings {
		numbers = append(numbers, posting.PostingNumber)
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT ON (w.posting_number) w.posting_number,w.work,r.observed_target FROM orders_work w JOIN orders_reconciliations r ON r.work_id=w.id AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=ANY($4::text[]) AND w.state='RECONCILED' AND w.action IN ('mirror_create','mirror_update','mirror_position_update') AND r.observed_target IS NOT NULL ORDER BY w.posting_number,r.created_at DESC,w.id DESC`, result.Config.Scope.Tenant, result.Config.Scope.MoyskladAccount, result.Config.Scope.OzonAccount, numbers)
	if err != nil {
		return ordersError("read reconciled position ownership", err)
	}
	defer rows.Close()
	result.PositionBaselines = make(map[string]orders.PositionBaseline, len(numbers))
	for rows.Next() {
		var number string
		var rawWork, rawTarget []byte
		if err = rows.Scan(&number, &rawWork, &rawTarget); err != nil {
			return ordersError("scan reconciled position ownership", err)
		}
		var work orders.Work
		var target orders.TargetObservation
		if err = ordersDecode(rawWork, &work); err != nil {
			return err
		}
		if err = ordersDecode(rawTarget, &target); err != nil {
			return err
		}
		if work.Scope != result.Config.Scope || target.Scope != result.Config.Scope || work.Command.PostingNumber != number {
			return orders.ErrStale
		}
		baseline, baselineErr := orders.ProjectionPositionBaseline(work, target)
		if baselineErr == nil {
			result.PositionBaselines[number] = baseline
		}
	}
	return ordersRowsError("read reconciled position ownership", rows.Err())
}

func validateOrderPositionBaseline(command orders.Command, current orders.ReviewContext) error {
	if command.Action != orders.ActionMirrorPositionUpdate {
		return nil
	}
	if command.PositionPatch == nil {
		return orders.ErrInvalid
	}
	baseline, found := current.PositionBaselines[command.PostingNumber]
	if !found || !baseline.Equal(command.PositionPatch.Baseline) {
		return orders.ErrStale
	}
	return nil
}
