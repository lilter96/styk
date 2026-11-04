package postgres

import (
	"context"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
	"time"
)

// One set-based read per selected batch; no provider I/O or network transaction.
// Only immutable successful reconciliation evidence can establish line ownership.
func enrichOrderPositionBaselines(ctx context.Context, q ordersQuerier, result *orders.ReviewContext) error {
	numbers := make([]string, 0, len(result.Postings))
	for _, posting := range result.Postings {
		numbers = append(numbers, posting.PostingNumber)
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT ON (w.posting_number) w.posting_number,w.work,r.observed_target,r.observed_native_document,n.observed_at FROM orders_work w JOIN orders_reconciliations r ON r.work_id=w.id AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account LEFT JOIN orders_native_document_results n ON n.work_id=w.id AND n.tenant_id=w.tenant_id AND n.moysklad_account=w.moysklad_account AND n.ozon_account=w.ozon_account WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=ANY($4::text[]) AND w.state='RECONCILED' AND w.action IN ('mirror_create','mirror_update','mirror_position_update','demand_create','salesreturn_create') AND (w.action IN ('mirror_create','mirror_update','mirror_position_update') OR COALESCE(w.work#>>'{command,native_movement,applicable,value}','true')<>'false') AND r.observed_target IS NOT NULL ORDER BY w.posting_number,r.created_at DESC,w.id DESC`, result.Config.Scope.Tenant, result.Config.Scope.MoyskladAccount, result.Config.Scope.OzonAccount, numbers)
	if err != nil {
		return ordersError("read reconciled position ownership", err)
	}
	defer rows.Close()
	result.PositionBaselines = make(map[string]orders.PositionBaseline, len(numbers))
	for rows.Next() {
		var number string
		var rawWork, rawTarget, rawNative []byte
		var confirmed *time.Time
		if err = rows.Scan(&number, &rawWork, &rawTarget, &rawNative, &confirmed); err != nil {
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
		baseline, baselineErr := readNativeOrMirrorBaseline(work, target, rawNative, confirmed)
		if baselineErr == nil {
			result.PositionBaselines[number] = baseline
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return ordersError("read reconciled position ownership", err)
	}
	return enrichRecoveryPositionBaselines(ctx, q, result, numbers)
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

func readNativeOrMirrorBaseline(work orders.Work, target orders.TargetObservation, raw []byte, confirmed *time.Time) (orders.PositionBaseline, error) {
	if work.Command.Action.IsMirror() {
		return orders.ProjectionPositionBaseline(work, target)
	}
	if confirmed == nil || len(raw) == 0 {
		return orders.PositionBaseline{}, orders.ErrBlocked
	}
	var native orders.NativeDocumentObservation
	if err := ordersDecode(raw, &native); err != nil {
		return orders.PositionBaseline{}, err
	}
	if !ordermoney.NativeParentEffectObserved(work.Command, target) || ordermoney.NativeDocumentTotal(work.Command, native).Code != "" {
		return orders.PositionBaseline{}, orders.ErrBlocked
	}
	return orders.NativePositionBaseline(work, target, native, *confirmed)
}
