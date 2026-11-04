package postgres

import (
	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
	"time"
)

// Two bounded indexed heads per posting: the latest readback and latest work
// capable of carrying/creating ownership. Legacy scalar updates are not owners.
const positionOwnershipSQL = `SELECT p.number,latest.work,latest.observed_target,owner.work,owner.observed_target,owner.observed_native_document,owner.confirmed FROM unnest($4::text[]) p(number)
 JOIN LATERAL(SELECT w.work,r.observed_target FROM orders_work w JOIN orders_reconciliations r ON r.work_id=w.id AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=p.number AND w.state='RECONCILED' AND w.action IN ('mirror_create','mirror_update','mirror_position_update','demand_create','salesreturn_create') AND r.observed_target IS NOT NULL AND (w.action IN ('mirror_create','mirror_update','mirror_position_update') OR COALESCE(w.work#>>'{command,native_movement,applicable,value}','true')<>'false') ORDER BY r.created_at DESC,w.id DESC LIMIT 1) latest ON true
 LEFT JOIN LATERAL(SELECT w.work,r.observed_target,r.observed_native_document,n.observed_at AS confirmed FROM orders_work w JOIN orders_reconciliations r ON r.work_id=w.id AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account LEFT JOIN orders_native_document_results n ON n.work_id=w.id AND n.tenant_id=w.tenant_id AND n.moysklad_account=w.moysklad_account AND n.ozon_account=w.ozon_account WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.posting_number=p.number AND w.state='RECONCILED' AND w.action IN ('mirror_create','mirror_update','mirror_position_update','demand_create') AND r.observed_target IS NOT NULL AND (w.action<>'mirror_update' OR (w.work#>'{command,position_baseline}' IS NOT NULL AND w.work#>'{command,position_baseline}'<>'null'::jsonb)) AND (w.action<>'demand_create' OR COALESCE(w.work#>>'{command,native_movement,applicable,value}','true')<>'false') ORDER BY r.created_at DESC,w.id DESC LIMIT 1) owner ON true ORDER BY p.number`

func readPositionOwnership(row pgx.Row, scope orders.Scope, now time.Time) (string, *orders.PositionBaseline, error) {
	var number string
	var rawLatest, rawTarget, rawOwner, rawOwnedTarget, rawNative []byte
	var confirmed *time.Time
	if err := row.Scan(&number, &rawLatest, &rawTarget, &rawOwner, &rawOwnedTarget, &rawNative, &confirmed); err != nil {
		return number, nil, ordersError("scan immutable position frontier", err)
	}
	var latest, owner orders.Work
	var target, ownedTarget orders.TargetObservation
	for _, pair := range []struct {
		raw []byte
		out any
	}{{rawLatest, &latest}, {rawTarget, &target}, {rawOwner, &owner}, {rawOwnedTarget, &ownedTarget}} {
		if len(pair.raw) == 0 {
			return number, nil, nil
		}
		if err := ordersDecode(pair.raw, pair.out); err != nil {
			return number, nil, err
		}
	}
	if latest.Scope != scope || owner.Scope != scope || target.Scope != scope || ownedTarget.Scope != scope || latest.Command.PostingNumber != number || owner.Command.PostingNumber != number {
		return number, nil, orders.ErrStale
	}
	return resolvePositionOwnership(number, latest, owner, target, ownedTarget, rawNative, confirmed, now)
}

func resolvePositionOwnership(number string, latest, owner orders.Work, target, ownedTarget orders.TargetObservation, rawNative []byte, confirmed *time.Time, now time.Time) (string, *orders.PositionBaseline, error) {
	if latest.Command.Action == orders.ActionSalesReturnCreate {
		return number, nil, nil
	}
	baseline, err := readNativeOrMirrorBaseline(owner, ownedTarget, rawNative, confirmed)
	if err != nil {
		return number, nil, nil
	}
	if baseline.RecoveryID != "" && !baseline.ValidUntil.After(now) {
		return number, nil, nil
	}
	if latest.ID == owner.ID {
		return number, &baseline, nil
	}
	if latest.Command.Action != orders.ActionMirrorUpdate || latest.Command.PositionBaseline != nil {
		return number, nil, nil
	}
	carried, err := orders.LegacyScalarPositionBaseline(latest, target, baseline)
	if err != nil {
		return number, nil, nil
	}
	return number, &carried, nil
}
