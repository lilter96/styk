package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

func (s *Store) ClaimCatalogWork(ctx context.Context, req catalogsync.ClaimRequest) (*catalogsync.Work, error) {
	if req.WorkerID == "" || !req.LeaseUntil.After(req.Now) || req.LeaseUntil.Sub(req.Now) > 5*time.Minute {
		return nil, catalogsync.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin catalog claim: %w", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = expireCatalogDispatches(ctx, tx, req.Now); err != nil {
		return nil, err
	}
	var raw []byte
	var taskID int64
	err = tx.QueryRow(ctx, `SELECT q.id,w.work FROM outbox_tasks q JOIN catalog_sync_work w ON w.id=q.catalog_work_id WHERE q.task_type='catalog_sync' AND ((q.state='pending' AND q.available_at<=$1) OR (q.state='running' AND q.lease_until<=$1)) AND w.state IN ('QUEUED','CLAIMED','PROCESSING','ACKNOWLEDGED') AND NOT EXISTS(SELECT 1 FROM catalog_sync_work prior WHERE prior.operation_id=w.operation_id AND prior.target_key=w.target_key AND prior.id<>w.id AND (prior.state IN ('CLAIMED','DISPATCHING','PROCESSING','ACKNOWLEDGED','UNKNOWN') OR (prior.state='QUEUED' AND prior.id<w.id))) ORDER BY q.available_at,q.id LIMIT 1 FOR UPDATE OF q,w SKIP LOCKED`, req.Now).Scan(&taskID, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit catalog expiry: %w", err)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim catalog work: %w", err)
	}
	w, err := fenceCatalogClaim(ctx, tx, req, taskID, raw)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit catalog claim: %w", err)
	}
	return &w, nil
}

func fenceCatalogClaim(ctx context.Context, tx pgx.Tx, req catalogsync.ClaimRequest, taskID int64, raw []byte) (catalogsync.Work, error) {
	var w catalogsync.Work
	if err := catalogDecode(raw, &w); err != nil {
		return catalogsync.Work{}, err
	}
	token, err := catalogID()
	if err != nil {
		return catalogsync.Work{}, err
	}
	w.ClaimToken = token
	if w.State == catalogsync.StateQueued {
		w.State = catalogsync.StateClaimed
	}
	if err = writeCatalogWork(ctx, tx, w, req.Now); err != nil {
		return catalogsync.Work{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE outbox_tasks SET state='running',attempts=1,claim_token=$2,lease_until=$3,updated_at=$4 WHERE id=$1`, taskID, token, req.LeaseUntil, req.Now); err != nil {
		return catalogsync.Work{}, fmt.Errorf("fence catalog claim: %w", err)
	}
	return w, nil
}

func expireCatalogDispatches(ctx context.Context, tx pgx.Tx, now time.Time) error {
	rows, err := tx.Query(ctx, `SELECT w.work,q.claim_token FROM outbox_tasks q JOIN catalog_sync_work w ON w.id=q.catalog_work_id WHERE q.task_type='catalog_sync' AND q.state='running' AND q.lease_until<=$1 AND w.state='DISPATCHING' ORDER BY q.id LIMIT 100 FOR UPDATE OF q,w SKIP LOCKED`, now)
	if err != nil {
		return fmt.Errorf("select expired catalog dispatches: %w", err)
	}
	works := make([]catalogsync.Work, 0, 100)
	for rows.Next() {
		var raw []byte
		var token string
		if err = rows.Scan(&raw, &token); err != nil {
			rows.Close()
			return fmt.Errorf("scan expired catalog dispatch: %w", err)
		}
		var w catalogsync.Work
		if err = catalogDecode(raw, &w); err != nil {
			rows.Close()
			return err
		}
		w.ClaimToken = token
		works = append(works, w)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return fmt.Errorf("read expired catalog dispatches: %w", err)
	}
	for _, w := range works {
		if err = expireCatalogWork(ctx, tx, w, now); err != nil {
			return err
		}
	}
	return nil
}

func expireCatalogWork(ctx context.Context, tx pgx.Tx, w catalogsync.Work, now time.Time) error {
	w.State = catalogsync.StateUnknown
	w.Code = "DISPATCH_LEASE_EXPIRED"
	if err := writeCatalogWork(ctx, tx, w, now); err != nil {
		return err
	}
	if err := insertCatalogAttempt(ctx, tx, w, "DISPATCH_LEASE_EXPIRED", now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE outbox_tasks SET state='failed',claim_token=NULL,lease_until=NULL,last_error='DISPATCH_LEASE_EXPIRED',updated_at=$2 WHERE task_type='catalog_sync' AND catalog_work_id=$1`, w.ID, now); err != nil {
		return fmt.Errorf("close unknown catalog dispatch: %w", err)
	}
	return nil
}

func lockCatalogWork(ctx context.Context, tx pgx.Tx, scope catalog.Scope, id, token string) (catalogsync.Work, error) {
	var raw []byte
	var w catalogsync.Work
	args := append(catalogScopeArgs(scope), id, token)
	err := tx.QueryRow(ctx, `SELECT w.work FROM catalog_sync_work w JOIN outbox_tasks q ON q.catalog_work_id=w.id WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.id=$4 AND q.task_type='catalog_sync' AND q.state='running' AND q.claim_token=$5 FOR UPDATE OF q,w`, args...).Scan(&raw)
	if err != nil {
		return w, catalogError("lock owned catalog work", err)
	}
	if err = catalogDecode(raw, &w); err != nil {
		return w, err
	}
	w.ClaimToken = token
	var unexpired bool
	err = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM outbox_tasks WHERE catalog_work_id=$1 AND task_type='catalog_sync'`, id).Scan(&unexpired)
	if err != nil {
		return w, fmt.Errorf("check catalog lease clock: %w", err)
	}
	if !unexpired {
		return w, catalogsync.ErrStale
	}
	return w, nil
}

func writeCatalogWork(ctx context.Context, tx pgx.Tx, w catalogsync.Work, now time.Time) error {
	raw, err := catalogJSON(w)
	if err != nil {
		return err
	}
	var finished any
	if catalogTerminal(w.State) {
		finished = now
	}
	tag, err := tx.Exec(ctx, `UPDATE catalog_sync_work SET state=$2,work=$3,finished_at=$4,updated_at=$5 WHERE id=$1`, w.ID, w.State, raw, finished, now)
	if err != nil {
		return fmt.Errorf("persist catalog work state: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return catalogsync.ErrNotFound
	}
	return nil
}

func catalogTerminal(state catalogsync.State) bool {
	return state == catalogsync.StateReconciled || state == catalogsync.StateRejected || state == catalogsync.StateBlocked || state == catalogsync.StateUnknown
}

func insertCatalogAttempt(ctx context.Context, tx pgx.Tx, w catalogsync.Work, code string, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO catalog_sync_attempts(work_id,tenant_id,moysklad_account,ozon_account,phase,claim_token,safe_code,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, w.ID, w.Scope.Tenant, w.Scope.MoyskladAccount, w.Scope.OzonAccount, w.State, w.ClaimToken, code, now)
	if err != nil {
		return fmt.Errorf("journal catalog attempt: %w", err)
	}
	return nil
}
