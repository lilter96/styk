package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) ApproveCarriagePreview(ctx context.Context, approval orders.CarriageApproval, _ time.Time) (orders.CarriageWork, error) {
	if approval.Scope.Validate() != nil || !ordersRequestKey(approval.IdempotencyKey) || approval.PreviewID == "" || !approval.AllReadyConsent || !approval.PhysicalReadinessConfirmed {
		return orders.CarriageWork{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.CarriageWork{}, ordersError("begin carriage approval", err)
	}
	defer rollbackMerchantMutation(tx)
	preview, err := readCarriagePreview(ctx, tx, approval.Scope, approval.PreviewID)
	if err != nil {
		return orders.CarriageWork{}, err
	}
	if err = lockCarriageTarget(ctx, tx, preview.Plan); err != nil {
		return orders.CarriageWork{}, err
	}
	if err = lockOrderReviewScope(ctx, tx, approval.Scope); err != nil {
		return orders.CarriageWork{}, err
	}
	owner, err := ordersActor(ctx, tx, approval.Scope.Tenant, approval.Actor)
	if err != nil {
		return orders.CarriageWork{}, err
	}
	work, err := approveCarriageLocked(ctx, tx, approval, preview, owner)
	if err != nil {
		return work, err
	}
	return work, ordersRowsError("commit carriage approval", tx.Commit(ctx))
}

func lockCarriageTarget(ctx context.Context, tx pgx.Tx, plan orders.CarriagePlan) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "orders-carriage:"+plan.Scope.OzonAccount+":"+plan.DeliveryMethodID)
	return ordersRowsError("lock global carriage target", err)
}

func approveCarriageLocked(ctx context.Context, tx pgx.Tx, approval orders.CarriageApproval, preview orders.CarriagePreview, owner string) (orders.CarriageWork, error) {
	hash, err := ordersHash(struct {
		Approval orders.CarriageApproval
		Actor    string
	}{approval, approval.Actor})
	if err != nil {
		return orders.CarriageWork{}, err
	}
	replay, err := replayCarriageApproval(ctx, tx, approval, hash)
	if err == nil {
		return replay, nil
	}
	if !errors.Is(err, orders.ErrNotFound) {
		return orders.CarriageWork{}, err
	}
	if preview.Actor != approval.Actor || len(preview.Validation) != 0 {
		return orders.CarriageWork{}, orders.ErrBlocked
	}
	if err = validateCarriagePreview(ctx, tx, preview); err != nil {
		return orders.CarriageWork{}, err
	}
	if err = validateCarriageHead(ctx, tx, preview.Plan); err != nil {
		return orders.CarriageWork{}, err
	}
	return insertCarriageWork(ctx, tx, approval, preview, owner, hash)
}

func replayCarriageApproval(ctx context.Context, q ordersQuerier, approval orders.CarriageApproval, hash string) (orders.CarriageWork, error) {
	var raw []byte
	var prior string
	err := q.QueryRow(ctx, `SELECT work,request_hash FROM orders_carriage_work WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND request_key=$4`, append(ordersScopeArgs(approval.Scope), approval.IdempotencyKey)...).Scan(&raw, &prior)
	if err != nil {
		return orders.CarriageWork{}, ordersError("read carriage approval replay", err)
	}
	if hash != prior {
		return orders.CarriageWork{}, orders.ErrConflict
	}
	var work orders.CarriageWork
	err = ordersDecode(raw, &work)
	return work, err
}

func validateCarriageHead(ctx context.Context, tx pgx.Tx, plan orders.CarriagePlan) error {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT w.work FROM orders_carriage_heads h JOIN orders_carriage_work w ON w.id=h.work_id WHERE h.ozon_account=$1 AND h.delivery_method_id=$2 FOR UPDATE OF h,w`, plan.Scope.OzonAccount, plan.DeliveryMethodID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return ordersError("read carriage target fence", err)
	}
	var prior orders.CarriageWork
	if err = ordersDecode(raw, &prior); err != nil {
		return err
	}
	if prior.Scope != plan.Scope {
		return orders.ErrBlocked
	}
	if prior.DispatchCount == 0 && prior.State.Terminal() {
		return nil
	}
	if prior.State == orders.StateReconciled && prior.Plan.Action == orders.CarriageCreate && plan.Action == orders.CarriageApprove && prior.CarriageID == plan.CarriageID {
		return nil
	}
	return orders.ErrBlocked
}

func insertCarriageWork(ctx context.Context, tx pgx.Tx, approval orders.CarriageApproval, preview orders.CarriagePreview, owner, hash string) (orders.CarriageWork, error) {
	id, err := ordersID()
	if err != nil {
		return orders.CarriageWork{}, err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return orders.CarriageWork{}, err
	}
	work := orders.CarriageWork{ID: id, Scope: approval.Scope, Plan: preview.Plan, PreviewID: preview.ID, ConfigRevision: preview.Plan.ConfigRevision, Credentials: preview.Plan.Credentials, State: orders.StateQueued, CarriageID: preview.Plan.CarriageID, NextRunAt: now, CreatedAt: now, UpdatedAt: now}
	raw, err := ordersJSON(work)
	if err != nil {
		return work, err
	}
	args := append(ordersScopeArgs(work.Scope), id, work.Plan.DeliveryMethodID, work.Plan.Action, work.CarriageID, work.PreviewID, work.ConfigRevision, work.State, raw, approval.Actor, owner, approval.IdempotencyKey, hash, now)
	_, err = tx.Exec(ctx, `INSERT INTO orders_carriage_work(tenant_id,moysklad_account,ozon_account,id,delivery_method_id,action,carriage_id,preview_id,config_revision,state,work,actor,owner_user_id,request_key,request_hash,all_ready_consent,physical_readiness_confirmed,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,true,true,$16,$16)`, args...)
	if err != nil {
		return work, ordersError("seal carriage command", err)
	}
	if err = enqueueCarriageWork(ctx, tx, work); err != nil {
		return work, err
	}
	return work, nil
}

func enqueueCarriageWork(ctx context.Context, tx pgx.Tx, work orders.CarriageWork) error {
	_, err := tx.Exec(ctx, `INSERT INTO orders_carriage_heads(ozon_account,delivery_method_id,work_id) VALUES($1,$2,$3) ON CONFLICT(ozon_account,delivery_method_id) DO UPDATE SET work_id=EXCLUDED.work_id`, work.Scope.OzonAccount, work.Plan.DeliveryMethodID, work.ID)
	if err != nil {
		return ordersError("bind carriage target fence", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_tasks(operation_key,task_type,carriage_work_id,carriage_tenant_id,carriage_moysklad_account,carriage_ozon_account,max_attempts,available_at) VALUES($1,'orders_carriage',$2,$3,$4,$5,1,$6)`, "orders-carriage:"+work.ID, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, work.NextRunAt)
	return ordersRowsError("enqueue carriage in existing outbox", err)
}
