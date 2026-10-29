package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/domain"
)

// ConfirmWarehouseReview atomically records the mapping, history, audit and retry outcome.
func (s *Store) ConfirmWarehouseReview(ctx context.Context, req domain.MappingConfirmationRequest) (domain.MappingConfirmationResult, error) {
	if err := req.Validate(); err != nil {
		return domain.MappingConfirmationResult{}, err
	}
	fingerprint, err := confirmationFingerprint(req)
	if err != nil {
		return domain.MappingConfirmationResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.MappingConfirmationResult{}, fmt.Errorf("begin warehouse confirmation: %w", err)
	}
	defer tx.Rollback(context.Background())
	stored, err := loadWarehouseReview(ctx, tx, req.ReviewID, req.SessionID)
	if err != nil {
		return domain.MappingConfirmationResult{}, err
	}
	if err = lockWarehouseReview(ctx, tx, stored.scope()); err != nil {
		return domain.MappingConfirmationResult{}, fmt.Errorf("lock warehouse confirmation scope: %w", err)
	}
	// Reconcile a committed result before expiry or revision checks, for this exact session and payload.
	var previousHash string
	var result domain.MappingConfirmationResult
	err = tx.QueryRow(ctx, `SELECT i.request_hash,a.mapping_id,a.mapping_version,a.review_id,a.evidence_class,a.confirmed_at FROM warehouse_mapping_confirmation_idempotency i JOIN warehouse_mapping_confirmation_audits a ON a.mapping_id=i.mapping_id AND a.mapping_version=i.mapping_version AND a.review_id=i.review_id AND a.session_audit_id=i.session_audit_id WHERE i.review_id=$1 AND i.session_audit_id=$2 AND i.idempotency_key=$3`, req.ReviewID, req.SessionID, req.IdempotencyKey).Scan(&previousHash, &result.MappingID, &result.MappingVersion, &result.ReviewID, &result.EvidenceClass, &result.ConfirmedAt)
	if err == nil {
		if previousHash != fingerprint {
			return domain.MappingConfirmationResult{}, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.MappingConfirmationResult{}, fmt.Errorf("finish warehouse confirmation replay: %w", err)
		}
		result.IdempotentReplay = true
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.MappingConfirmationResult{}, fmt.Errorf("read warehouse confirmation outcome: %w", err)
	}
	if !req.Acknowledged {
		return domain.MappingConfirmationResult{}, domain.ErrInvalidMappingConfirmation
	}
	r := stored.review
	if !time.Now().Before(r.ExpiresAt) {
		return domain.MappingConfirmationResult{}, ErrReviewExpired
	}
	status, snapshot, err := evaluateWarehouseReview(ctx, tx, stored)
	if err != nil {
		return domain.MappingConfirmationResult{}, fmt.Errorf("revalidate warehouse review: %w", err)
	}
	switch status {
	case "EXPIRED":
		return domain.MappingConfirmationResult{}, ErrReviewExpired
	case "STALE_SOURCE":
		return domain.MappingConfirmationResult{}, ErrReviewStale
	case "STALE_MAPPING":
		if snapshot.Conflict {
			return domain.MappingConfirmationResult{}, ErrMappingConflict
		}
		return domain.MappingConfirmationResult{}, ErrReviewStale
	case "CURRENT":
	default:
		return domain.MappingConfirmationResult{}, errors.New("unknown warehouse review status")
	}
	// Existing pairs cannot be replaced or attached to a second review.
	if snapshot.Conflict || snapshot.MappingID != nil {
		return domain.MappingConfirmationResult{}, ErrMappingConflict
	}
	if snapshot.IsRFBS == nil || *snapshot.IsRFBS {
		return domain.MappingConfirmationResult{}, ErrReviewStale
	}
	if !time.Now().Before(r.ExpiresAt) {
		return domain.MappingConfirmationResult{}, ErrReviewExpired
	}
	var id int64
	var version int
	var confirmed time.Time
	err = tx.QueryRow(ctx, `INSERT INTO warehouse_mappings(tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,version,provenance,confirmed_at) VALUES($1,$2,$3,$4,$5,'active',1,$6,clock_timestamp()) RETURNING id,version,confirmed_at`, r.Tenant, r.MoyskladAccount, r.OzonAccount, r.MoyskladWarehouseID, r.OzonWarehouseID, auditPrincipal(req.SessionID)).Scan(&id, &version, &confirmed)
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.TableName == "warehouse_mappings" {
			return domain.MappingConfirmationResult{}, ErrMappingConflict
		}
		return domain.MappingConfirmationResult{}, fmt.Errorf("insert warehouse mapping: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO warehouse_mapping_history(mapping_id,version,tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,provenance,confirmed_at,moysklad_import_id,ozon_import_id,confirmation_review_id) VALUES($1,$2,$3,$4,$5,$6,$7,'active',$12,$8,$9,$10,$11)`, id, version, r.Tenant, r.MoyskladAccount, r.OzonAccount, r.MoyskladWarehouseID, r.OzonWarehouseID, confirmed, r.MoyskladImportID, r.OzonImportID, r.ID, auditPrincipal(req.SessionID))
	if err != nil {
		return domain.MappingConfirmationResult{}, fmt.Errorf("insert warehouse mapping history: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO warehouse_mapping_confirmation_audits(mapping_id,mapping_version,review_id,tenant_id,ozon_account,moysklad_account,moysklad_warehouse_id,ozon_warehouse_id,ozon_import_id,moysklad_import_id,evidence_class,principal_type,session_audit_id,explanation,confirmed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'OPERATOR_ATTESTED',$14,$11,$12,$13)`, id, version, r.ID, r.Tenant, r.OzonAccount, r.MoyskladAccount, r.MoyskladWarehouseID, r.OzonWarehouseID, r.OzonImportID, r.MoyskladImportID, req.SessionID, req.Explanation, confirmed, auditPrincipal(req.SessionID))
	if err != nil {
		return domain.MappingConfirmationResult{}, fmt.Errorf("insert warehouse confirmation audit: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO warehouse_mapping_confirmation_idempotency(review_id,session_audit_id,idempotency_key,request_hash,mapping_id,mapping_version) VALUES($1,$2,$3,$4,$5,$6)`, r.ID, req.SessionID, req.IdempotencyKey, fingerprint, id, version)
	if err != nil {
		return domain.MappingConfirmationResult{}, fmt.Errorf("persist warehouse confirmation outcome: %w", err)
	}
	if !time.Now().Before(r.ExpiresAt) {
		return domain.MappingConfirmationResult{}, ErrReviewExpired
	}
	if err = tx.Commit(ctx); err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23514" && pgerr.ConstraintName == "warehouse_review_unexpired" {
			return domain.MappingConfirmationResult{}, ErrReviewExpired
		}
		return domain.MappingConfirmationResult{}, fmt.Errorf("commit warehouse confirmation: %w", err)
	}
	return domain.MappingConfirmationResult{MappingID: id, MappingVersion: version, ReviewID: r.ID, EvidenceClass: domain.MappingOperatorAttested, ConfirmedAt: confirmed}, nil
}
