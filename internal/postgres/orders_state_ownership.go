package postgres

import (
	"context"
	"slices"

	"github.com/lilter96/styk/internal/orders"
)

const nativeStateOwnershipSQL = `SELECT p.number,latest.work,latest.observed_target
 FROM unnest($4::text[]) p(number)
 LEFT JOIN LATERAL (
 SELECT w.work,r.observed_target FROM orders_work w
 JOIN orders_reconciliations r ON r.work_id=w.id AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=p.number AND w.state='RECONCILED'
 AND (w.action='mirror_create' OR (w.action='mirror_update' AND w.work#>'{command,customer_order,owned_fields}' @> '["state"]'::jsonb))
 AND r.observed_target IS NOT NULL ORDER BY r.created_at DESC,w.id DESC LIMIT 1
 ) latest ON true ORDER BY p.number`

func enrichOrderNativeStateBaselines(ctx context.Context, q ordersQuerier, result *orders.ReviewContext, numbers []string) error {
	rows, err := q.Query(ctx, nativeStateOwnershipSQL, result.Config.Scope.Tenant, result.Config.Scope.MoyskladAccount, result.Config.Scope.OzonAccount, numbers)
	if err != nil {
		return ordersError("read native state provenance", err)
	}
	defer rows.Close()
	result.NativeStateBaselines = make(map[string]orders.NativeStateBaseline, len(numbers))
	for rows.Next() {
		var number string
		var rawWork, rawTarget []byte
		if err = rows.Scan(&number, &rawWork, &rawTarget); err != nil {
			return ordersError("scan native state provenance", err)
		}
		if len(rawWork) == 0 || len(rawTarget) == 0 {
			continue
		}
		baseline, readErr := decodeNativeStateBaseline(rawWork, rawTarget)
		if readErr != nil {
			return readErr
		}
		if baseline != nil && baseline.Scope == result.Config.Scope && baseline.Correlation.PostingNumber == number {
			result.NativeStateBaselines[number] = *baseline
		}
	}
	return ordersRowsError("read native state provenance", rows.Err())
}

func decodeNativeStateBaseline(rawWork, rawTarget []byte) (*orders.NativeStateBaseline, error) {
	var work orders.Work
	var target orders.TargetObservation
	if err := ordersDecode(rawWork, &work); err != nil {
		return nil, err
	}
	if err := ordersDecode(rawTarget, &target); err != nil {
		return nil, err
	}
	baseline, err := orders.ProjectionNativeStateBaseline(work, target)
	if err != nil {
		return nil, nil
	}
	return &baseline, nil
}

func validateOrderNativeStateBaseline(command orders.Command, current orders.ReviewContext) error {
	if command.Action == orders.ActionMirrorCreate {
		if command.NativeStateBaseline != nil {
			return orders.ErrInvalid
		}
		return nil
	}
	if command.CustomerOrder == nil || !slices.Contains(command.CustomerOrder.OwnedFields, "state") {
		if command.NativeStateBaseline != nil {
			return orders.ErrInvalid
		}
		return nil
	}
	if current.Config.Lifecycle != nil && current.Config.Lifecycle.Enabled {
		return orders.ErrBlocked
	}
	baseline, found := current.NativeStateBaselines[command.PostingNumber]
	if !found || command.NativeStateBaseline == nil || !baseline.Equal(*command.NativeStateBaseline) || baseline.Validate() != nil || baseline.Correlation != command.Correlation || baseline.TargetRevision != command.ExpectedTargetRevision {
		return orders.ErrStale
	}
	return nil
}
