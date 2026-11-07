package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) ApproveCommissionPreview(ctx context.Context, approval orders.CommissionApproval, _ time.Time) (orders.CommissionWork, error) {
	if approval.Scope.Validate() != nil || approval.PreviewID == "" || !ordersRequestKey(approval.IdempotencyKey) {
		return orders.CommissionWork{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.CommissionWork{}, ordersError("begin commission approval", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockOrderReviewScope(ctx, tx, approval.Scope); err != nil {
		return orders.CommissionWork{}, err
	}
	work, err := approveCommissionLocked(ctx, tx, approval)
	if err != nil {
		return work, err
	}
	return work, ordersRowsError("commit commission approval", tx.Commit(ctx))
}

func approveCommissionLocked(ctx context.Context, tx pgx.Tx, approval orders.CommissionApproval) (orders.CommissionWork, error) {
	config, err := readOrderConfig(ctx, tx, approval.Scope)
	if err != nil {
		return orders.CommissionWork{}, err
	}
	owner, err := commissionOwner(ctx, tx, config, approval.Actor, approval.Automatic)
	if err != nil {
		return orders.CommissionWork{}, err
	}
	hash, err := ordersHash(struct {
		Approval orders.CommissionApproval
		Actor    string
		Auto     bool
	}{approval, approval.Actor, approval.Automatic})
	if err != nil {
		return orders.CommissionWork{}, err
	}
	replay, err := replayCommissionApproval(ctx, tx, approval, hash)
	if err == nil {
		return replay, nil
	}
	if !errors.Is(err, orders.ErrNotFound) {
		return orders.CommissionWork{}, err
	}
	preview, err := readCommissionPreview(ctx, tx, approval.Scope, approval.PreviewID)
	if err != nil {
		return orders.CommissionWork{}, err
	}
	if preview.Actor != approval.Actor || preview.Automatic != approval.Automatic || len(preview.Validation) != 0 {
		return orders.CommissionWork{}, orders.ErrBlocked
	}
	if err = validateCommissionPreview(ctx, tx, preview, config, owner); err != nil {
		return orders.CommissionWork{}, err
	}
	if err = validateCommissionRetry(ctx, tx, approval, preview.Plan.Source.Period); err != nil {
		return orders.CommissionWork{}, err
	}
	return insertCommissionWork(ctx, tx, approval, preview, owner, hash)
}

func replayCommissionApproval(ctx context.Context, q ordersQuerier, approval orders.CommissionApproval, hash string) (orders.CommissionWork, error) {
	var id, prior string
	err := q.QueryRow(ctx, `SELECT id,request_hash FROM orders_finance_work WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND request_key=$4`, append(ordersScopeArgs(approval.Scope), approval.IdempotencyKey)...).Scan(&id, &prior)
	if err != nil {
		return orders.CommissionWork{}, ordersError("read commission approval replay", err)
	}
	if prior != hash {
		return orders.CommissionWork{}, orders.ErrConflict
	}
	return readCommissionWork(ctx, q, approval.Scope, id)
}

func validateCommissionRetry(ctx context.Context, q ordersQuerier, approval orders.CommissionApproval, period orders.RealizationPeriod) error {
	existing, err := readCommissionPeriod(ctx, q, approval.Scope, period)
	if err != nil {
		return err
	}
	if existing == nil {
		if approval.RetryWorkID != "" {
			return orders.ErrInvalid
		}
		return nil
	}
	if approval.Automatic || approval.RetryWorkID != existing.ID || !existing.CanRetryKnownUnsent() {
		return orders.ErrBlocked
	}
	var fenced bool
	err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_finance_work WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND report_year=$4 AND report_month=$5 AND (dispatch_count<>0 OR work->>'code' IN('FINANCE_EXISTING_DOCUMENT_REQUIRES_REVIEW','FINANCE_EXISTING_DOCUMENT_UNVERIFIED')))`, append(ordersScopeArgs(approval.Scope), period.Year, period.Month)...).Scan(&fenced)
	if err != nil {
		return ordersError("verify commission retry has no earlier dispatch or native identity conflict", err)
	}
	if fenced {
		return orders.ErrBlocked
	}
	return nil
}

func insertCommissionWork(ctx context.Context, tx pgx.Tx, approval orders.CommissionApproval, preview orders.CommissionPreview, owner, hash string) (orders.CommissionWork, error) {
	id, err := ordersID()
	if err != nil {
		return orders.CommissionWork{}, err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return orders.CommissionWork{}, err
	}
	work := orders.CommissionWork{ID: id, Scope: approval.Scope, Plan: preview.Plan, PreviewID: preview.ID, ConfigRevision: preview.Plan.ConfigRevision, Credentials: preview.Plan.Credentials, State: orders.StateQueued, Automatic: approval.Automatic, NextRunAt: now, CreatedAt: now, UpdatedAt: now}
	raw, err := ordersJSON(work)
	if err != nil {
		return work, err
	}
	args := append(ordersScopeArgs(work.Scope), id, work.Plan.Source.Period.Year, work.Plan.Source.Period.Month, work.PreviewID, work.ConfigRevision, work.State, raw, approval.Actor, owner, approval.IdempotencyKey, hash, approval.RetryWorkID, now)
	_, err = tx.Exec(ctx, `INSERT INTO orders_finance_work(tenant_id,moysklad_account,ozon_account,id,report_year,report_month,preview_id,config_revision,state,work,actor,owner_user_id,request_key,request_hash,predecessor_work_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NULLIF($15,''),$16,$16)`, args...)
	if err != nil {
		return work, ordersError("seal approved commission draft", err)
	}
	if err = bindCommissionPeriod(ctx, tx, work); err != nil {
		return work, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_tasks(operation_key,task_type,finance_work_id,finance_tenant_id,finance_moysklad_account,finance_ozon_account,max_attempts,available_at) VALUES($1,'orders_finance',$2,$3,$4,$5,1,$6)`, "orders-finance:"+id, id, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, now)
	return work, ordersRowsError("enqueue commission draft in shared outbox", err)
}

func bindCommissionPeriod(ctx context.Context, tx pgx.Tx, work orders.CommissionWork) error {
	args := append(ordersScopeArgs(work.Scope), work.Plan.Source.Period.Year, work.Plan.Source.Period.Month, work.Plan.Source.Fingerprint, work.ID)
	_, err := tx.Exec(ctx, `INSERT INTO orders_finance_periods(tenant_id,moysklad_account,ozon_account,report_year,report_month,source_fingerprint,current_fingerprint,work_id) VALUES($1,$2,$3,$4,$5,$6,$6,$7) ON CONFLICT(tenant_id,moysklad_account,ozon_account,report_year,report_month) DO UPDATE SET source_fingerprint=$6,current_fingerprint=$6,source_changed=false,work_id=$7`, args...)
	return ordersRowsError("bind canonical commission period", err)
}
