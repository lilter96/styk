package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalogsync"
)

func (s *Store) BeginCatalogReconciliation(ctx context.Context, req catalogsync.ReconciliationRequest, now time.Time) (catalogsync.ReconciliationClaim, error) {
	if req.Scope.Validate() != nil || !catalogTestScope(req.Scope) || !req.Acknowledge || req.WorkID == "" || len(req.IdempotencyKey) == 0 || len(req.IdempotencyKey) > 128 || len(req.Reason) < 5 || len(req.Reason) > 1000 {
		return catalogsync.ReconciliationClaim{}, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	defer rollbackMerchantMutation(tx)
	if err = catalogActor(ctx, tx, req.Scope.Tenant, req.Actor); err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	if err = lockCatalogDispatchScope(ctx, tx, req.Scope); err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	hash, err := catalogHash(req)
	if err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	replay, err := replayCatalogReadback(ctx, tx, req, hash)
	if err != nil {
		return replay, err
	}
	if replay.Result != nil {
		return replay, nil
	}
	work, err := lockUnknownCatalogWork(ctx, tx, req)
	if err != nil {
		return replay, err
	}
	if err = expireCatalogReadback(ctx, tx, work.ID, now); err != nil {
		return replay, err
	}
	claim, err := insertCatalogReadback(ctx, tx, req, work, hash, now)
	if err != nil {
		return replay, err
	}
	if err = tx.Commit(ctx); err != nil {
		return replay, fmt.Errorf("commit catalog readback intent: %w", err)
	}
	return claim, nil
}

func replayCatalogReadback(ctx context.Context, tx pgx.Tx, req catalogsync.ReconciliationRequest, hash string) (catalogsync.ReconciliationClaim, error) {
	var id, previous string
	var raw []byte
	args := append(catalogScopeArgs(req.Scope), req.Actor, req.IdempotencyKey)
	err := tx.QueryRow(ctx, `SELECT r.id,r.request_hash,x.result FROM catalog_sync_readbacks r LEFT JOIN catalog_sync_readback_results x ON x.request_id=r.id WHERE r.tenant_id=$1 AND r.moysklad_account=$2 AND r.ozon_account=$3 AND r.actor=$4 AND r.request_key=$5`, args...).Scan(&id, &previous, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return catalogsync.ReconciliationClaim{}, nil
	}
	if err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	if previous != hash {
		return catalogsync.ReconciliationClaim{}, catalogsync.ErrConflict
	}
	if raw == nil {
		return catalogsync.ReconciliationClaim{}, catalogsync.ErrConflict
	}
	var result catalogsync.ReconciliationResult
	if err = catalogDecode(raw, &result); err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	return catalogsync.ReconciliationClaim{ID: id, Result: &result}, nil
}

func lockUnknownCatalogWork(ctx context.Context, tx pgx.Tx, req catalogsync.ReconciliationRequest) (catalogsync.Work, error) {
	var work catalogsync.Work
	var raw []byte
	var inactive bool
	args := append(catalogScopeArgs(req.Scope), req.WorkID)
	err := tx.QueryRow(ctx, `SELECT w.work,q.state IN ('failed','cancelled') AND q.claim_token IS NULL AND q.lease_until IS NULL FROM catalog_sync_work w JOIN outbox_tasks q ON q.catalog_work_id=w.id AND q.task_type='catalog_sync' WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.id=$4 FOR UPDATE OF w,q`, args...).Scan(&raw, &inactive)
	if err != nil {
		return work, catalogError("read owned unknown catalog work", err)
	}
	if err = catalogDecode(raw, &work); err != nil {
		return work, err
	}
	if !inactive || work.State != catalogsync.StateUnknown {
		return work, catalogsync.ErrBlocked
	}
	return work, nil
}

func expireCatalogReadback(ctx context.Context, tx pgx.Tx, workID string, now time.Time) error {
	var requestID string
	var active bool
	err := tx.QueryRow(ctx, `SELECT request_id,expires_at>clock_timestamp() FROM catalog_sync_readback_claims WHERE work_id=$1 FOR UPDATE`, workID).Scan(&requestID, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if active {
		return catalogsync.ErrConflict
	}
	result := catalogsync.ReconciliationResult{ID: requestID, WorkID: workID, State: catalogsync.StateUnknown, Code: "READBACK_LEASE_EXPIRED", Completed: true}
	if err = saveCatalogReadbackResult(ctx, tx, result, now); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM catalog_sync_readback_claims WHERE work_id=$1`, workID)
	return err
}

func insertCatalogReadback(ctx context.Context, tx pgx.Tx, req catalogsync.ReconciliationRequest, work catalogsync.Work, hash string, now time.Time) (catalogsync.ReconciliationClaim, error) {
	id, err := catalogID()
	if err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	token, err := catalogID()
	if err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	raw, err := catalogJSON(work)
	if err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	original, err := catalogHash(work)
	if err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	args := append(catalogScopeArgs(req.Scope), id, work.ID, req.Actor, req.IdempotencyKey, hash, req.Reason, req.AcceptWarnings, raw, original, now)
	_, err = tx.Exec(ctx, `INSERT INTO catalog_sync_readbacks(tenant_id,moysklad_account,ozon_account,id,work_id,actor,request_key,request_hash,reason,accepted_warnings,original_work,original_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, args...)
	if err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO catalog_sync_readback_claims(work_id,request_id,token,expires_at) VALUES($1,$2,$3,$4)`, work.ID, id, token, now.Add(time.Minute)); err != nil {
		return catalogsync.ReconciliationClaim{}, err
	}
	work.ClaimToken = token
	return catalogsync.ReconciliationClaim{ID: id, Token: token, Work: work}, nil
}

func (s *Store) CompleteCatalogReconciliation(ctx context.Context, claim catalogsync.ReconciliationClaim, c catalogsync.Completion) (catalogsync.ReconciliationResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return catalogsync.ReconciliationResult{}, err
	}
	defer rollbackMerchantMutation(tx)
	if err = lockCatalogDispatchScope(ctx, tx, c.Scope); err != nil {
		return catalogsync.ReconciliationResult{}, err
	}
	work, err := lockCatalogReadbackCompletion(ctx, tx, claim, c)
	if err != nil {
		return catalogsync.ReconciliationResult{}, err
	}
	if err = validateCatalogCompletion(work, c); err != nil {
		return catalogsync.ReconciliationResult{}, err
	}
	now, err := catalogClock(ctx, tx, c.Now)
	if err != nil {
		return catalogsync.ReconciliationResult{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('styk.catalog_readback_token',$1,true)`, claim.Token); err != nil {
		return catalogsync.ReconciliationResult{}, err
	}
	if err = saveManualCatalogEffect(ctx, tx, work, c, now); err != nil {
		return catalogsync.ReconciliationResult{}, err
	}
	result := catalogsync.ReconciliationResult{ID: claim.ID, WorkID: work.ID, State: c.State, Code: c.Code, Completed: true}
	if err = saveCatalogReadbackResult(ctx, tx, result, now); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM catalog_sync_readback_claims WHERE request_id=$1 AND token=$2`, claim.ID, claim.Token); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func lockCatalogReadbackCompletion(ctx context.Context, tx pgx.Tx, claim catalogsync.ReconciliationClaim, c catalogsync.Completion) (catalogsync.Work, error) {
	var work catalogsync.Work
	var actor, original string
	var accepted bool
	var raw []byte
	if c.ExpectedState != catalogsync.StateUnknown || (c.State != catalogsync.StateUnknown && c.State != catalogsync.StateReconciled) || c.WorkID != claim.Work.ID || c.Scope != claim.Work.Scope || c.ClaimToken != claim.Token {
		return work, catalogsync.ErrStale
	}
	args := append(catalogScopeArgs(c.Scope), c.WorkID, claim.ID, claim.Token)
	err := tx.QueryRow(ctx, `SELECT w.work,r.actor,r.original_hash,r.accepted_warnings FROM catalog_sync_work w JOIN catalog_sync_readbacks r ON r.work_id=w.id AND r.tenant_id=w.tenant_id AND r.moysklad_account=w.moysklad_account AND r.ozon_account=w.ozon_account JOIN catalog_sync_readback_claims l ON l.work_id=w.id AND l.request_id=r.id WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.id=$4 AND r.id=$5 AND l.token=$6 AND l.expires_at>clock_timestamp() FOR UPDATE OF w,l`, args...).Scan(&raw, &actor, &original, &accepted)
	if err != nil {
		return work, catalogError("lock scoped catalog readback", err)
	}
	if err = catalogActor(ctx, tx, c.Scope.Tenant, actor); err != nil {
		return work, err
	}
	if err = catalogDecode(raw, &work); err != nil {
		return work, err
	}
	hash, err := catalogHash(work)
	if err != nil {
		return work, err
	}
	if work.State != catalogsync.StateUnknown || hash != original {
		return work, catalogsync.ErrStale
	}
	if err = validateReadbackEvidence(work, c, accepted); err != nil {
		return work, err
	}
	return work, nil
}

func validateReadbackEvidence(work catalogsync.Work, c catalogsync.Completion, accepted bool) error {
	if c.State != catalogsync.StateReconciled {
		return nil
	}
	if c.Receipt.TaskID != work.Receipt.TaskID || !catalogsync.ReconciliationReceiptPermitted(work.Command, c.Receipt, accepted) {
		return catalogsync.ErrStale
	}
	return nil
}

func saveManualCatalogEffect(ctx context.Context, tx pgx.Tx, work catalogsync.Work, c catalogsync.Completion, now time.Time) error {
	work.State, work.Code = c.State, c.Code
	if c.Receipt.State != "" {
		work.Receipt = sanitizeCatalogReceipt(c.Receipt)
	}
	if c.CreatedTarget != nil {
		work.CreatedTarget = c.CreatedTarget
	}
	if err := writeCatalogWork(ctx, tx, work, now); err != nil {
		return err
	}
	if err := insertCatalogAttempt(ctx, tx, work, "MANUAL_READBACK_"+c.Code, now); err != nil {
		return err
	}
	if err := saveCatalogCompletionEvidence(ctx, tx, work, c, now); err != nil {
		return err
	}
	if c.State == catalogsync.StateReconciled {
		return rebaseCatalogSiblings(ctx, tx, work, c, now)
	}
	return nil
}

func saveCatalogReadbackResult(ctx context.Context, tx pgx.Tx, result catalogsync.ReconciliationResult, now time.Time) error {
	raw, err := catalogJSON(result)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO catalog_sync_readback_results(request_id,work_id,result,completed_at) VALUES($1,$2,$3,$4)`, result.ID, result.WorkID, raw, now)
	return err
}
