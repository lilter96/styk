package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/testpublication"
)

// TouchTestStockWorker makes running worker instances visible to the recovery
// gate. An expired heartbeat is not proof that an in-flight HTTP request ended.
func (s *Store) TouchTestStockWorker(ctx context.Context, workerID string, now time.Time) error {
	scope := testpublication.Scope()
	if !testpublication.Enabled(scope) || len(workerID) != 32 {
		return domain.ErrStockSyncInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = syncLock(ctx, tx, scope); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO stock_test_worker_presence(tenant_id,moysklad_account,ozon_account,worker_id,expires_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(tenant_id,moysklad_account,ozon_account,worker_id) DO UPDATE SET expires_at=EXCLUDED.expires_at`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, workerID, now.Add(15*time.Second))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SupersedeTestUnknown records the operator's accepted uncertainty before
// releasing only the auxiliary fence. It never mutates or retries the old write.
func (s *Store) SupersedeTestUnknown(ctx context.Context, req domain.StockRecoveryRequest, now time.Time) (domain.StockSyncView, error) {
	if !testpublication.Enabled(req.StockSyncScope) || !req.Acknowledge || !req.WorkerStopped || len(req.OperationID) != 32 || req.SessionID == "" || len(req.IdempotencyKey) < 16 || len(req.IdempotencyKey) > 128 || len(strings.TrimSpace(req.Reason)) < 16 || len(req.Reason) > 1000 || strings.ContainsRune(req.Reason, 0) || req.ExpectedReadbackAt.IsZero() || !testpublication.AutomaticQuantityAllowed(req.ExpectedQuantity) {
		return domain.StockSyncView{}, domain.ErrStockSyncInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.StockSyncView{}, err
	}
	defer tx.Rollback(context.Background())
	if err = syncLock(ctx, tx, req.StockSyncScope); err != nil {
		return domain.StockSyncView{}, err
	}
	hash := syncHash(struct {
		Request                domain.StockRecoveryRequest
		OperationID, SessionID string
	}{req, req.OperationID, req.SessionID})
	var existing string
	err = tx.QueryRow(ctx, `SELECT request_hash FROM stock_publication_recoveries WHERE tenant_id=$1 AND idempotency_key=$2`, req.Tenant, req.IdempotencyKey).Scan(&existing)
	if err == nil {
		if existing != hash {
			return domain.StockSyncView{}, domain.ErrStockSyncConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.StockSyncView{}, err
		}
		return s.StockSyncStatus(ctx, req.StockSyncScope, now)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.StockSyncView{}, err
	}
	var targetID, readbackID int64
	var product, warehouse, state, taskState string
	var observed *string
	var attempts int
	var observedAt time.Time
	err = tx.QueryRow(ctx, `SELECT t.id,t.product_id,t.warehouse_id,t.state,q.state,q.attempts FROM stock_publication_targets t JOIN stock_publications p ON p.id=t.operation_id JOIN outbox_tasks q ON q.publication_operation_id=p.id WHERE p.id=$1 AND p.tenant_id=$2 AND p.moysklad_account=$3 AND p.ozon_account=$4 FOR UPDATE OF t,q`, req.OperationID, req.Tenant, req.MoyskladAccount, req.OzonAccount).Scan(&targetID, &product, &warehouse, &state, &taskState, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StockSyncView{}, domain.ErrStockSyncNotFound
	}
	if err != nil {
		return domain.StockSyncView{}, err
	}
	var enrolled bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_auto_cycle_publications cp JOIN stock_auto_cycles c ON c.id=cp.cycle_id JOIN stock_auto_config_audits a ON a.tenant_id=c.tenant_id AND a.moysklad_account=c.moysklad_account AND a.ozon_account=c.ozon_account AND a.revision=c.config_revision WHERE cp.publication_id=$1 AND c.tenant_id=$2 AND c.moysklad_account=$3 AND c.ozon_account=$4 AND cp.product_id=$5 AND cp.warehouse_id=$6 AND a.enabled AND a.snapshot->'targets' @> jsonb_build_array(jsonb_build_object('ozon_product_id',$5::text,'ozon_warehouse_id',$6::text)))`, req.OperationID, req.Tenant, req.MoyskladAccount, req.OzonAccount, product, warehouse).Scan(&enrolled); err != nil {
		return domain.StockSyncView{}, err
	}
	legacy := product == testpublication.OzonProduct && warehouse == testpublication.OzonWarehouse
	if state != "UNKNOWN" || taskState != "failed" || attempts != 1 || (!legacy && !enrolled) {
		return domain.StockSyncView{}, domain.ErrStockSyncBlocked
	}
	var alreadyRecovered bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_publication_recoveries WHERE target_id=$1)`, targetID).Scan(&alreadyRecovered); err != nil {
		return domain.StockSyncView{}, err
	}
	if alreadyRecovered {
		return domain.StockSyncView{}, domain.ErrStockSyncBlocked
	}
	err = tx.QueryRow(ctx, `SELECT id,quantity,observed_at FROM stock_publication_readbacks WHERE target_id=$1 ORDER BY observed_at DESC,id DESC LIMIT 1`, targetID).Scan(&readbackID, &observed, &observedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StockSyncView{}, domain.ErrStockSyncBlocked
	}
	if err != nil {
		return domain.StockSyncView{}, err
	}
	if observed == nil || *observed != req.ExpectedQuantity || !observedAt.Equal(req.ExpectedReadbackAt) || observedAt.After(now) || now.Sub(observedAt) > 2*time.Minute {
		return domain.StockSyncView{}, domain.ErrStockSyncStale
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_test_worker_presence WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND expires_at>$4) OR EXISTS(SELECT 1 FROM stock_publication_targets WHERE ozon_account=$3 AND product_id=$5 AND warehouse_id=$6 AND state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED'))`, req.Tenant, req.MoyskladAccount, req.OzonAccount, now, product, warehouse).Scan(&active)
	if err != nil {
		return domain.StockSyncView{}, err
	}
	if active {
		return domain.StockSyncView{}, domain.ErrStockSyncBlocked
	}
	id, err := stockSyncID()
	if err != nil {
		return domain.StockSyncView{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO stock_publication_recoveries(id,target_id,tenant_id,moysklad_account,ozon_account,product_id,warehouse_id,readback_id,observed_quantity,observed_at,principal_type,session_audit_id,idempotency_key,request_hash,reason,accepted_uncertainty,worker_stopped_assertion,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'LOCAL_OPERATOR_SESSION',$11,$12,$13,$14,true,true,$15)`, id, targetID, req.Tenant, req.MoyskladAccount, req.OzonAccount, product, warehouse, readbackID, *observed, observedAt, req.SessionID, req.IdempotencyKey, hash, strings.TrimSpace(req.Reason), now)
	if err != nil {
		return domain.StockSyncView{}, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM stock_publication_target_fences WHERE target_id=$1`, targetID)
	if err != nil {
		return domain.StockSyncView{}, err
	}
	if tag.RowsAffected() != 1 {
		return domain.StockSyncView{}, domain.ErrStockSyncBlocked
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.StockSyncView{}, err
	}
	return s.StockSyncStatus(ctx, req.StockSyncScope, now)
}
