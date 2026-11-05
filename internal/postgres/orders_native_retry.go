package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

// The canonical event is shared across Ozon scopes belonging to one native
// account. Hold its row lock only inside this short database transaction.
func guardNativeRetryEvent(ctx context.Context, tx pgx.Tx, work orders.Work, created time.Time) error {
	if work.Command.NativeMovement.Retry == nil {
		if err := insertNativeCanonicalEvent(ctx, tx, work, created); err != nil {
			return err
		}
	}
	if err := lockNativeCanonicalEvent(ctx, tx, work); err != nil {
		return err
	}
	checked, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	return validateNativeConsumptionIntent(ctx, tx, work, checked)
}

func insertNativeCanonicalEvent(ctx context.Context, tx pgx.Tx, work orders.Work, created time.Time) error {
	m := work.Command.NativeMovement
	_, err := tx.Exec(ctx, `INSERT INTO orders_native_document_events
 (tenant_id,moysklad_account,kind,event_digest,sync_id,marker,ozon_account,posting_number,customer_order_id,origin_work_id,created_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT DO NOTHING`, work.Scope.Tenant, work.Scope.MoyskladAccount, m.Correlation.Kind, m.Event.Digest, m.Correlation.SyncID, m.Correlation.Marker, work.Scope.OzonAccount, work.Command.PostingNumber, work.Command.Correlation.CustomerOrderID, work.ID, created)
	return ordersRowsError("reserve canonical native physical event", err)
}

func lockNativeCanonicalEvent(ctx context.Context, tx pgx.Tx, work orders.Work) error {
	m := work.Command.NativeMovement
	var origin, account, posting, parent, syncID, marker string
	err := tx.QueryRow(ctx, `SELECT origin_work_id,ozon_account,posting_number,customer_order_id,sync_id::text,marker
 FROM orders_native_document_events WHERE tenant_id=$1 AND moysklad_account=$2 AND kind=$3 AND event_digest=$4 FOR UPDATE`, work.Scope.Tenant, work.Scope.MoyskladAccount, m.Correlation.Kind, m.Event.Digest).Scan(&origin, &account, &posting, &parent, &syncID, &marker)
	if err != nil {
		return ordersError("lock canonical native physical event", err)
	}
	if account != work.Scope.OzonAccount || posting != work.Command.PostingNumber || parent != work.Command.Correlation.CustomerOrderID || syncID != m.Correlation.SyncID || marker != m.Correlation.Marker {
		return orders.ErrConflict
	}
	if m.Retry == nil && origin != work.ID || m.Retry != nil && origin == work.ID {
		return orders.ErrConflict
	}
	return nil
}

func insertNativeRetryAuthorization(ctx context.Context, tx pgx.Tx, work orders.Work) error {
	m := work.Command.NativeMovement
	if m.Retry == nil {
		return nil
	}
	tag, err := tx.Exec(ctx, `INSERT INTO orders_native_document_retries
 (work_id,previous_work_id,tenant_id,moysklad_account,ozon_account,kind,event_digest,operation_id,owner_user_id,actor,created_at)
 SELECT w.id,$5,w.tenant_id,w.moysklad_account,w.ozon_account,$6,$7,w.operation_id,o.owner_user_id,o.actor,w.created_at
 FROM orders_work w JOIN orders_operations o ON o.id=w.operation_id AND o.tenant_id=w.tenant_id
 AND o.moysklad_account=w.moysklad_account AND o.ozon_account=w.ozon_account
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.id=$4`, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, work.ID, m.Retry.PreviousWorkID, m.Correlation.Kind, m.Event.Digest)
	if err != nil {
		return ordersError("audit explicit native rejection retry", err)
	}
	if tag.RowsAffected() != 1 {
		return orders.ErrStale
	}
	return nil
}
