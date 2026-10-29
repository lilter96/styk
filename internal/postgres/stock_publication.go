package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
)

func sameSyncConditions(a, b domain.StockSyncReview) bool {
	if a.MoyskladCredentialRevision != b.MoyskladCredentialRevision || a.OzonCredentialRevision != b.OzonCredentialRevision || len(a.Targets) != len(b.Targets) {
		return false
	}
	for i, t := range a.Targets {
		v := b.Targets[i]
		if t.ProductID != v.ProductID || t.WarehouseID != v.WarehouseID || t.SourceItemID != v.SourceItemID || t.SourceWarehouseID != v.SourceWarehouseID || t.ProductMappingVersion != v.ProductMappingVersion || t.WarehouseMappingVersion != v.WarehouseMappingVersion {
			return false
		}
		ap, bp := t.Decision.Evidence.ProductMapping, v.Decision.Evidence.ProductMapping
		aw, bw := t.Decision.Evidence.WarehouseMapping, v.Decision.Evidence.WarehouseMapping
		if ap == nil || bp == nil || aw == nil || bw == nil || ap.ID != bp.ID || aw.ID != bw.ID {
			return false
		}

	}
	return true
}

// RefreshStockSync reevaluates at most five enabled scopes every five seconds.
// It never imports source data, fabricates proofs or writes to a marketplace.
// Only a new eligible persisted evidence version and changed quantity can
// enqueue another bounded publication while the original controls remain valid.
func (s *Store) RefreshStockSync(ctx context.Context, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	_, err := s.Pool.Exec(ctx, `UPDATE stock_publication_targets SET state='UNKNOWN',safe_error='WORKER_OUTCOME_UNKNOWN' WHERE state='DISPATCHING' AND dispatched_at<$1`, now.Add(-time.Minute))
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id,moysklad_account,ozon_account FROM stock_sync_settings WHERE enabled AND last_proof_check_at<$1 ORDER BY last_proof_check_at LIMIT 5`, now.Add(-5*time.Second))
	if err != nil {
		return err
	}
	var scopes []domain.StockSyncScope
	for rows.Next() {
		var scope domain.StockSyncScope
		if err = rows.Scan(&scope.Tenant, &scope.MoyskladAccount, &scope.OzonAccount); err != nil {
			rows.Close()
			return err
		}
		scopes = append(scopes, scope)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		if err = s.refreshStockScope(ctx, scope, now); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) refreshStockScope(ctx context.Context, scope domain.StockSyncScope, now time.Time) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err = syncLock(ctx, tx, scope); err != nil {
		return err
	}
	var enabled bool
	var reviewID, key string
	err = tx.QueryRow(ctx, `SELECT enabled,review_id,last_evaluation_key FROM stock_sync_settings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 FOR UPDATE`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&enabled, &reviewID, &key)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	base, err := readSyncReview(ctx, tx, scope, reviewID)
	if err != nil {
		return err
	}
	current, err := s.stockSyncReview(ctx, tx, scope, base.Attestations, now)
	if err != nil {
		return err
	}
	if !sameSyncConditions(base, current) {
		current.Ready = false
		current.Checks = append(current.Checks, domain.StockSyncCheck{Code: "SYNC_CONDITIONS_CHANGED", Message: "Изменились соответствия или данные подключения. Повторно проверьте условия отправки."})
	}
	checks, _ := json.Marshal(current.Checks)
	if _, err = tx.Exec(ctx, `UPDATE stock_sync_settings SET last_proof_check_at=$4,last_checks=$5::jsonb WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now, checks); err != nil {
		return err
	}
	if !current.Ready {
		return tx.Commit(ctx)
	}
	currentKey := stableSyncEvidence(current)
	if currentKey == key {
		return tx.Commit(ctx)
	}
	var pending bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_publication_targets t JOIN stock_publications p ON p.id=t.operation_id WHERE p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3 AND t.state IN ('QUEUED','DISPATCHING','UNKNOWN'))`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&pending)
	if err != nil {
		return err
	}
	if pending {
		return tx.Commit(ctx)
	}
	changed := false
	for _, t := range current.Targets {
		var old string
		err = tx.QueryRow(ctx, `SELECT quantity FROM stock_publication_targets WHERE ozon_account=$1 AND product_id=$2 AND warehouse_id=$3 AND state IN ('SUCCEEDED','CONFIRMED_EFFECTIVE') ORDER BY finished_at DESC,id DESC LIMIT 1`, scope.OzonAccount, t.ProductID, t.WarehouseID).Scan(&old)
		if errors.Is(err, pgx.ErrNoRows) {
			changed = true
		} else if err != nil {
			return err
		} else if t.Quantity != nil && old != *t.Quantity {
			changed = true
		}
	}
	if changed {
		current.ID, err = stockSyncID()
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(current)
		_, err = tx.Exec(ctx, `INSERT INTO stock_sync_reviews(id,tenant_id,moysklad_account,ozon_account,ready,snapshot,created_at,expires_at) VALUES($1,$2,$3,$4,true,$5::jsonb,$6,$7)`, current.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, raw, now, current.ExpiresAt)
		if err != nil {
			return err
		}
		if _, err = createPublication(ctx, tx, current, now); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE stock_sync_settings SET last_evaluation_key=$4 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, currentKey)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// NextStockPublication atomically fences a write before I/O. Dispatching claims
// have no lease retry path. A crash can become UNKNOWN, never QUEUED again.
func (s *Store) NextStockPublication(ctx context.Context, now time.Time) (*domain.StockPublication, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	var scope domain.StockSyncScope
	var opID string
	err = tx.QueryRow(ctx, `SELECT p.id,p.tenant_id,p.moysklad_account,p.ozon_account FROM stock_publications p JOIN stock_sync_settings s USING(tenant_id,moysklad_account,ozon_account) WHERE s.enabled AND EXISTS(SELECT 1 FROM stock_publication_targets t WHERE t.operation_id=p.id AND t.state='QUEUED') AND NOT EXISTS(SELECT 1 FROM stock_publication_dispatch_clock clock WHERE clock.ozon_account=p.ozon_account AND clock.dispatched_at>$1::timestamptz-interval '1 second') AND NOT EXISTS(SELECT 1 FROM stock_publication_targets queued JOIN stock_publication_targets prior ON prior.ozon_account=queued.ozon_account AND prior.product_id=queued.product_id AND prior.warehouse_id=queued.warehouse_id WHERE queued.operation_id=p.id AND queued.state='QUEUED' AND prior.dispatched_at>$1::timestamptz-interval '30 seconds') ORDER BY p.created_at,p.id LIMIT 1`, now).Scan(&opID, &scope.Tenant, &scope.MoyskladAccount, &scope.OzonAccount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = syncLock(ctx, tx, scope); err != nil {
		return nil, err
	}
	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM stock_sync_settings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 FOR UPDATE`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&enabled); err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	var queued bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_publication_targets WHERE operation_id=$1 AND state='QUEUED')`, opID).Scan(&queued); err != nil {
		return nil, err
	}
	if !queued {
		return nil, nil
	}
	op, err := readPublication(ctx, tx, scope, opID)
	if err != nil {
		return nil, err
	}
	review, err := readSyncReview(ctx, tx, scope, op.ReviewID)
	if err != nil {
		return nil, err
	}
	current, err := s.stockSyncReview(ctx, tx, scope, review.Attestations, now)
	if err != nil {
		return nil, err
	}
	if !review.Ready || !review.ExpiresAt.After(now) || !current.Ready || stableSyncEvidence(current) != stableSyncEvidence(review) {
		_, err = tx.Exec(ctx, `UPDATE stock_publication_targets SET state='BLOCKED',safe_error='EVIDENCE_CHANGED_OR_EXPIRED',finished_at=$2 WHERE operation_id=$1 AND state='QUEUED'`, opID, now)
		if err != nil {
			return nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO stock_publication_dispatch_clock(ozon_account,dispatched_at) VALUES($1,$2) ON CONFLICT DO NOTHING`, scope.OzonAccount, now.Add(-time.Hour))
	if err != nil {
		return nil, err
	}
	var last time.Time
	if err = tx.QueryRow(ctx, `SELECT dispatched_at FROM stock_publication_dispatch_clock WHERE ozon_account=$1 FOR UPDATE`, scope.OzonAccount).Scan(&last); err != nil {
		return nil, err
	}
	if now.Sub(last) < time.Second {
		return nil, nil
	}
	for _, t := range op.Targets {
		if t.State != "QUEUED" {
			continue
		}
		var recent bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_publication_targets WHERE ozon_account=$1 AND product_id=$2 AND warehouse_id=$3 AND dispatched_at>$4)`, scope.OzonAccount, t.ProductID, t.WarehouseID, now.Add(-30*time.Second)).Scan(&recent)
		if err != nil {
			return nil, err
		}
		if recent {
			return nil, nil
		}
	}
	_, err = tx.Exec(ctx, `UPDATE stock_publication_dispatch_clock SET dispatched_at=$2 WHERE ozon_account=$1`, scope.OzonAccount, now)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE stock_publication_targets SET state='DISPATCHING',dispatched_at=$2 WHERE operation_id=$1 AND state='QUEUED'`, opID, now)
	if err != nil {
		return nil, err
	}
	op, err = readPublication(ctx, tx, scope, opID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &op, nil
}
func (s *Store) StockPublicationStillEnabled(ctx context.Context, scope domain.StockSyncScope) (bool, error) {
	var enabled bool
	err := s.Pool.QueryRow(ctx, `SELECT enabled FROM stock_sync_settings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount).Scan(&enabled)
	return enabled, err
}
func (s *Store) FinishStockTarget(ctx context.Context, scope domain.StockSyncScope, operationID string, targetID int64, state, code string, now time.Time) error {
	if state != "SUCCEEDED" && state != "FAILED" && state != "UNKNOWN" && state != "BLOCKED" && state != "CANCELLED" && state != "CONFIRMED_EFFECTIVE" {
		return errors.New("invalid publication result")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	allowed := "DISPATCHING"
	if state == "CONFIRMED_EFFECTIVE" {
		allowed = "UNKNOWN"
	}
	query := `UPDATE stock_publication_targets t SET state=$6,safe_error=$7,finished_at=$8 FROM stock_publications p WHERE t.operation_id=p.id AND p.id=$4 AND t.id=$5 AND p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3 AND t.state=$9`
	args := []any{scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, operationID, targetID, state, code, now, allowed}
	if state == "CONFIRMED_EFFECTIVE" {
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(context.Background())
		if err = syncLock(ctx, tx, scope); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrStockSyncStale
		}
		if _, err = tx.Exec(ctx, `UPDATE stock_sync_settings SET enabled=false,updated_at=$4 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, now); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	tag, err := s.Pool.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrStockSyncStale
	}
	return nil
}

func (s *Store) StockPublication(ctx context.Context, scope domain.StockSyncScope, id string) (domain.StockPublication, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.StockPublication{}, err
	}
	defer tx.Rollback(context.Background())
	return readPublication(ctx, tx, scope, id)
}
func (s *Store) MarkStockPublicationUnknown(ctx context.Context, scope domain.StockSyncScope, id string, now time.Time) error {
	_, err := s.Pool.Exec(ctx, `UPDATE stock_publication_targets t SET state='UNKNOWN',safe_error='WORKER_OUTCOME_UNKNOWN' FROM stock_publications p WHERE t.operation_id=p.id AND p.id=$4 AND p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3 AND t.state='DISPATCHING' AND t.dispatched_at<$5`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, id, now.Add(-time.Minute))
	return err
}

func StockSyncPublic(value any) any {
	raw, _ := json.Marshal(value)
	var data any
	_ = json.Unmarshal(raw, &data)
	var clean func(any)
	clean = func(v any) {
		switch item := v.(type) {
		case map[string]any:
			delete(item, "decision")
			delete(item, "attestations")
			delete(item, "moysklad_credential_revision")
			delete(item, "ozon_credential_revision")
			if state, ok := item["state"].(string); ok {
				switch state {
				case "DISPATCHING", "UPDATING":
					item["state"] = "RUNNING"
				case "UNKNOWN":
					item["state"] = "OUTCOME_UNKNOWN"
				}
			}
			for _, child := range item {
				clean(child)
			}
		case []any:
			for _, child := range item {
				clean(child)
			}
		}
	}
	clean(data)
	return data
}

// StockPublicationCanDispatch is the final pre-I/O check, after credentials were
// resolved. No transaction remains open during the network operation.
func (s *Store) StockPublicationCanDispatch(ctx context.Context, op domain.StockPublication, now time.Time) (bool, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT enabled FROM stock_sync_settings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, op.Tenant, op.MoyskladAccount, op.OzonAccount).Scan(&enabled); err != nil {
		return false, err
	}
	if !enabled {
		return false, nil
	}
	review, err := readSyncReview(ctx, tx, op.StockSyncScope, op.ReviewID)
	if err != nil {
		return false, err
	}
	if !review.ExpiresAt.After(now) {
		return false, nil
	}
	current, err := s.stockSyncReview(ctx, tx, op.StockSyncScope, review.Attestations, now)
	if err != nil {
		return false, err
	}
	return current.Ready && stableSyncEvidence(current) == stableSyncEvidence(review), nil
}

// RefreshStockSyncScope is a bounded internal reevaluation hook for one known
// scope; it has no HTTP route and never starts an import or external request.
func (s *Store) RefreshStockSyncScope(ctx context.Context, scope domain.StockSyncScope, now time.Time) error {
	if !scope.Valid() {
		return domain.ErrStockSyncNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return s.refreshStockScope(ctx, scope, now)
}
