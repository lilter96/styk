package postgres

import (
	"context"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) PendingMerchantIntents(ctx context.Context, scope orders.Scope, limit int) ([]orders.MerchantIntentStatus, error) {
	return s.readMerchantIntents(ctx, scope, limit, true)
}

func (s *Store) OrderMerchantIntents(ctx context.Context, scope orders.Scope, limit int) ([]orders.MerchantIntentStatus, error) {
	return s.readMerchantIntents(ctx, scope, limit, false)
}

func (s *Store) readMerchantIntents(ctx context.Context, scope orders.Scope, limit int, pending bool) ([]orders.MerchantIntentStatus, error) {
	if scope.Validate() != nil || limit < 1 || limit > 100 {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	rows, err := s.Pool.Query(ctx, `SELECT intent,state,code,COALESCE(work_id::text,'') FROM orders_merchant_intents i WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND (NOT $4 OR (state='OBSERVED' AND NOT EXISTS(SELECT 1 FROM orders_target_fences f WHERE f.tenant_id=i.tenant_id AND f.moysklad_account=i.moysklad_account AND f.ozon_account=i.ozon_account AND f.posting_number=i.posting_number)
 AND (i.intent->>'action' NOT IN ('demand_create','salesreturn_create') OR NOT EXISTS(SELECT 1 FROM orders_merchant_intents peer WHERE peer.tenant_id=i.tenant_id AND peer.moysklad_account=i.moysklad_account AND peer.ozon_account=i.ozon_account AND peer.posting_number=i.posting_number AND peer.customer_order_id=i.customer_order_id AND peer.intent->>'after_revision'=i.intent->>'after_revision' AND peer.intent->>'action' NOT IN ('demand_create','salesreturn_create') AND peer.state='OBSERVED'))))
 ORDER BY CASE WHEN $4 THEN recorded_at END ASC,recorded_at DESC,CASE WHEN intent->>'action' IN ('demand_create','salesreturn_create') THEN 1 ELSE 0 END,id DESC LIMIT $5`, append(ordersScopeArgs(scope), pending, limit)...)
	if err != nil {
		return nil, ordersError("read merchant intent journal", err)
	}
	defer rows.Close()
	result := make([]orders.MerchantIntentStatus, 0, limit)
	for rows.Next() {
		var item orders.MerchantIntentStatus
		var raw []byte
		if err = rows.Scan(&raw, &item.State, &item.Code, &item.WorkID); err != nil {
			return nil, ordersError("scan merchant intent", err)
		}
		if err = ordersDecode(raw, &item.Intent); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, ordersRowsError("read merchant intent journal", rows.Err())
}

func (s *Store) BlockMerchantIntent(ctx context.Context, scope orders.Scope, id, code string) error {
	if scope.Validate() != nil || id == "" || code == "" || len(code) > 128 {
		return orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	_, err := s.Pool.Exec(ctx, `UPDATE orders_merchant_intents SET state='BLOCKED',code=$5 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND state='OBSERVED'`, append(ordersScopeArgs(scope), id, code)...)
	return ordersRowsError("record merchant intent intervention", err)
}

func (s *Store) WaitMerchantIntent(ctx context.Context, scope orders.Scope, id, code string) error {
	if scope.Validate() != nil || id == "" || code == "" || len(code) > 128 {
		return orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	_, err := s.Pool.Exec(ctx, `UPDATE orders_merchant_intents SET code=$5 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND state='OBSERVED'`, append(ordersScopeArgs(scope), id, code)...)
	return ordersRowsError("record merchant read awaiting retry", err)
}

func validateMerchantIntentJournal(ctx context.Context, q ordersQuerier, command orders.Command, workID string) error {
	if command.MerchantIntent == nil {
		return nil
	}
	var raw []byte
	var state, bound string
	err := q.QueryRow(ctx, `SELECT intent,state,COALESCE(work_id::text,'') FROM orders_merchant_intents WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4`, append(ordersScopeArgs(command.Scope), command.MerchantIntent.ID)...).Scan(&raw, &state, &bound)
	if err != nil {
		return ordersError("verify recorded merchant intent", err)
	}
	var recorded orders.MerchantIntent
	if err = ordersDecode(raw, &recorded); err != nil {
		return err
	}
	if !reflect.DeepEqual(recorded, *command.MerchantIntent) || state != "OBSERVED" && (state != "QUEUED" || workID == "" || bound != workID) {
		return orders.ErrBlocked
	}
	if err = validateMerchantPeerOrder(ctx, q, command); err != nil {
		return err
	}
	return validateMerchantIntentLatestPoint(ctx, q, command)
}

func validateMerchantPeerOrder(ctx context.Context, q ordersQuerier, command orders.Command) error {
	if !command.Action.IsNativeMovement() {
		return nil
	}
	var pending bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_merchant_intents WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=$4 AND customer_order_id=$5 AND intent->>'after_revision'=$6 AND intent->>'action' NOT IN ('demand_create','salesreturn_create') AND state='OBSERVED')`, append(ordersScopeArgs(command.Scope), command.PostingNumber, command.Correlation.CustomerOrderID, command.MerchantIntent.AfterRevision)...).Scan(&pending)
	if err != nil {
		return ordersError("verify merchant command sequence", err)
	}
	if pending {
		return orders.ErrBlocked
	}
	return nil
}

func validateMerchantIntentLatestPoint(ctx context.Context, q ordersQuerier, command orders.Command) error {
	var revision, state, nativeID string
	var credential int64
	err := q.QueryRow(ctx, `SELECT COALESCE(s.target_revision,''),COALESCE(s.point->>'state_id',''),r.native_order_id,r.credential_revision
 FROM orders_native_point_reads r LEFT JOIN orders_native_point_snapshots s ON s.id=r.snapshot_id
 WHERE r.tenant_id=$1 AND r.moysklad_account=$2 AND r.ozon_account=$3 AND r.posting_number=$4
 ORDER BY r.admitted_at DESC,r.finished_at DESC,r.id DESC LIMIT 1`, append(ordersScopeArgs(command.Scope), command.PostingNumber)...).Scan(&revision, &state, &nativeID, &credential)
	if err != nil {
		return ordersError("verify latest native merchant state", err)
	}
	intent := command.MerchantIntent
	if revision != intent.AfterRevision || state != intent.AfterStateID || nativeID != intent.Correlation.CustomerOrderID || credential != intent.CredentialRevision {
		return orders.ErrStale
	}
	return nil
}

func bindMerchantIntentWork(ctx context.Context, tx pgx.Tx, work orders.Work) error {
	if work.Command.MerchantIntent == nil {
		return nil
	}
	if err := validateMerchantIntentJournal(ctx, tx, work.Command, ""); err != nil {
		return err
	}
	args := append(ordersScopeArgs(work.Scope), work.Command.MerchantIntent.ID, work.ID)
	tag, err := tx.Exec(ctx, `UPDATE orders_merchant_intents SET state='QUEUED',code='MERCHANT_COMMAND_QUEUED',work_id=$5 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4 AND state='OBSERVED'`, args...)
	if err != nil {
		return ordersError("bind merchant intent to existing outbox work", err)
	}
	if tag.RowsAffected() != 1 {
		return orders.ErrConflict
	}
	return nil
}
