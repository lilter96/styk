package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/matching"
)

var (
	ErrReviewExpired       = errors.New("review expired")
	ErrReviewStale         = errors.New("review stale")
	ErrMappingConflict     = errors.New("mapping conflict")
	ErrIdempotencyConflict = errors.New("idempotency key reused with different request")
)

// confirmationFingerprint covers semantic request content, with a version so
// its canonical format can evolve without silently changing retry semantics.
func confirmationFingerprint(req domain.MappingConfirmationRequest) (string, error) {
	b, err := json.Marshal(struct {
		Version      int    `json:"version"`
		ReviewID     string `json:"review_id"`
		Acknowledged bool   `json:"acknowledged"`
		Explanation  string `json:"explanation"`
	}{1, req.ReviewID, req.Acknowledged, req.Explanation})
	if err != nil {
		return "", fmt.Errorf("encode confirmation fingerprint: %w", err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// ConfirmMappingReview uses the review's locks and source-revision policy. The
// mapping, review-bound history, audit and retry outcome commit together.
func (s *Store) ConfirmMappingReview(ctx context.Context, req domain.MappingConfirmationRequest) (domain.MappingConfirmationResult, error) {
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
		return domain.MappingConfirmationResult{}, fmt.Errorf("begin confirmation: %w", err)
	}
	defer tx.Rollback(context.Background())
	stored, err := loadMappingReview(ctx, tx, req.ReviewID, req.SessionID)
	if err != nil {
		return domain.MappingConfirmationResult{}, err
	}
	if err = lockReview(ctx, tx, stored.scope()); err != nil {
		return domain.MappingConfirmationResult{}, fmt.Errorf("lock confirmation scope: %w", err)
	}

	// A committed result remains replayable even after the review expires or its
	// imports change. It cannot be used by a different authenticated session.
	var previousHash string
	var result domain.MappingConfirmationResult
	err = tx.QueryRow(ctx, `SELECT i.request_hash,a.mapping_id,a.mapping_version,a.review_id,a.evidence_class,a.confirmed_at FROM product_mapping_confirmation_idempotency i JOIN product_mapping_confirmation_audits a ON a.mapping_id=i.mapping_id AND a.mapping_version=i.mapping_version AND a.review_id=i.review_id AND a.session_audit_id=i.session_audit_id WHERE i.review_id=$1 AND i.session_audit_id=$2 AND i.idempotency_key=$3`, req.ReviewID, req.SessionID, req.IdempotencyKey).Scan(&previousHash, &result.MappingID, &result.MappingVersion, &result.ReviewID, &result.EvidenceClass, &result.ConfirmedAt)
	if err == nil {
		if previousHash != fingerprint {
			return domain.MappingConfirmationResult{}, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.MappingConfirmationResult{}, fmt.Errorf("finish confirmation replay: %w", err)
		}
		result.IdempotentReplay = true
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.MappingConfirmationResult{}, fmt.Errorf("read confirmation outcome: %w", err)
	}
	if !req.Acknowledged {
		return domain.MappingConfirmationResult{}, domain.ErrInvalidMappingConfirmation
	}
	r := stored.review
	if !time.Now().Before(r.ExpiresAt) {
		return domain.MappingConfirmationResult{}, ErrReviewExpired
	}
	if r.EvidenceClass != domain.MappingOperatorAttested && r.EvidenceClass != domain.MappingDeterministicCandidate {
		return domain.MappingConfirmationResult{}, domain.ErrInvalidMappingConfirmation
	}
	status, snap, err := evaluateMappingReview(ctx, tx, stored)
	if errors.Is(err, ErrReviewScope) {
		return domain.MappingConfirmationResult{}, ErrReviewStale
	}
	if err != nil {
		return domain.MappingConfirmationResult{}, fmt.Errorf("revalidate review: %w", err)
	}
	switch status {
	case "EXPIRED":
		return domain.MappingConfirmationResult{}, ErrReviewExpired
	case "STALE_SOURCE", "STALE_MAPPING":
		return domain.MappingConfirmationResult{}, ErrReviewStale
	case "CURRENT":
	default:
		return domain.MappingConfirmationResult{}, fmt.Errorf("unknown review status")
	}
	if strings.TrimSpace(snap.Offer) == "" {
		return domain.MappingConfirmationResult{}, ErrReviewStale
	}
	// Existing mappings, including the same target, require no new confirmation;
	// replacement and attaching a second review are deliberately unsupported.
	if snap.Conflict || snap.MappingID != nil {
		return domain.MappingConfirmationResult{}, ErrMappingConflict
	}
	if r.EvidenceClass == domain.MappingDeterministicCandidate {
		src := domain.MatchSource{Identity: r.Source, OfferID: snap.Offer}
		target := domain.MatchTarget{Identity: r.Target, Article: snap.Article, Code: snap.Code}
		if len(matching.New([]domain.MatchTarget{target}, nil).Match(r.Tenant, src, r.Target.Account).Candidates) == 0 {
			return domain.MappingConfirmationResult{}, ErrReviewStale
		}
	}
	if !time.Now().Before(r.ExpiresAt) {
		return domain.MappingConfirmationResult{}, ErrReviewExpired
	}
	m, err := insertConfirmedMapping(ctx, tx, mappingInsert{Scope: stored.scope(), ProductID: r.Source.ID, OfferID: snap.Offer, TargetKind: r.Target.Kind, TargetID: r.Target.ID, Provenance: "LOCAL_OPERATOR_SESSION", OzonImportID: r.OzonImportID, MoyskladImportID: r.MoyskladImportID, ReviewID: &r.ID})
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.TableName == "product_mappings" {
			return domain.MappingConfirmationResult{}, ErrMappingConflict
		}
		return domain.MappingConfirmationResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_mapping_confirmation_audits(mapping_id,mapping_version,review_id,tenant_id,ozon_account,moysklad_account,ozon_product_id,ozon_offer_id,target_kind,target_id,ozon_import_id,moysklad_import_id,evidence_class,principal_type,session_audit_id,explanation,confirmed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'LOCAL_OPERATOR_SESSION',$14,$15,$16)`, m.ID, m.Version, r.ID, r.Tenant, r.Source.Account, r.Target.Account, r.Source.ID, snap.Offer, r.Target.Kind, r.Target.ID, r.OzonImportID, r.MoyskladImportID, r.EvidenceClass, req.SessionID, req.Explanation, m.ConfirmedAt)
	if err != nil {
		return domain.MappingConfirmationResult{}, fmt.Errorf("insert confirmation audit: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_mapping_confirmation_idempotency(review_id,session_audit_id,idempotency_key,request_hash,mapping_id,mapping_version) VALUES($1,$2,$3,$4,$5,$6)`, r.ID, req.SessionID, req.IdempotencyKey, fingerprint, m.ID, m.Version)
	if err != nil {
		return domain.MappingConfirmationResult{}, fmt.Errorf("persist confirmation outcome: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23514" && pgerr.ConstraintName == "product_review_unexpired" {
			return domain.MappingConfirmationResult{}, ErrReviewExpired
		}
		return domain.MappingConfirmationResult{}, fmt.Errorf("commit mapping confirmation: %w", err)
	}
	return domain.MappingConfirmationResult{MappingID: m.ID, MappingVersion: m.Version, ReviewID: r.ID, EvidenceClass: r.EvidenceClass, ConfirmedAt: m.ConfirmedAt}, nil
}
