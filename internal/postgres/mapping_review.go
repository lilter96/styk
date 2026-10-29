package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"example.com/styk/internal/domain"
	"example.com/styk/internal/matching"
	"github.com/jackc/pgx/v5"
)

var ErrReviewScope = errors.New("review scope not registered")
var ErrReviewSource = errors.New("review source not found")
var ErrReviewConflict = errors.New("review mapping conflict")
var ErrReviewEvidence = errors.New("invalid review evidence")
var ErrReviewMissing = errors.New("review not found")

type reviewSnapshot struct {
	OzonID, MoyID                                                                                     int64
	OzonImportFingerprint, MoyImportFingerprint                                                       string
	Offer                                                                                             string
	SKU                                                                                               *string
	Archived                                                                                          *bool
	Name, Article, Code, Parent, Updated                                                              string
	MappingTargetAccount, MappingKind, MappingTarget, MappingOffer, MappingProduct, MappingProvenance string
	MappingConfirmed                                                                                  *time.Time
	MappingID                                                                                         *int64
	MappingVersion                                                                                    *int
	Conflict                                                                                          bool
}

func lockReview(ctx context.Context, tx pgx.Tx, scope MatchScope) error {
	for _, key := range []string{"ozon:" + scope.OzonAccount, "moysklad:" + scope.MoyskladAccount, "matching:" + scope.Tenant} {
		if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); e != nil {
			return e
		}
	}
	return nil
}
func snapshotReview(ctx context.Context, tx pgx.Tx, in domain.MappingReviewRequest) (reviewSnapshot, error) {
	var s reviewSnapshot
	var ok bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='ozon' AND account_id=$2) AND EXISTS(SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='moysklad' AND account_id=$3)`, in.Tenant, in.OzonAccount, in.MoyskladAccount).Scan(&ok); e != nil {
		return s, e
	}
	if !ok {
		return s, ErrReviewScope
	}
	e := tx.QueryRow(ctx, `SELECT i.id,i.fingerprint FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source='ozon' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1 FOR SHARE OF i`, in.Tenant, in.OzonAccount).Scan(&s.OzonID, &s.OzonImportFingerprint)
	if errors.Is(e, pgx.ErrNoRows) {
		return s, ErrReviewSource
	}
	if e != nil {
		return s, e
	}
	e = tx.QueryRow(ctx, `SELECT i.id,i.fingerprint FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source='moysklad' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1 FOR SHARE OF i`, in.Tenant, in.MoyskladAccount).Scan(&s.MoyID, &s.MoyImportFingerprint)
	if errors.Is(e, pgx.ErrNoRows) {
		return s, ErrReviewSource
	}
	if e != nil {
		return s, e
	}
	e = tx.QueryRow(ctx, `SELECT offer_id,sku,archived FROM ozon_product_observations WHERE import_id=$1 AND product_id=$2 FOR SHARE`, s.OzonID, in.ProductID).Scan(&s.Offer, &s.SKU, &s.Archived)
	if errors.Is(e, pgx.ErrNoRows) {
		return s, ErrReviewSource
	}
	if e != nil {
		return s, e
	}
	e = tx.QueryRow(ctx, `SELECT name,article,code,parent_id,source_updated FROM source_entities WHERE import_id=$1 AND kind=$2 AND external_id=$3 FOR SHARE`, s.MoyID, in.TargetKind, in.TargetID).Scan(&s.Name, &s.Article, &s.Code, &s.Parent, &s.Updated)
	if errors.Is(e, pgx.ErrNoRows) {
		return s, ErrReviewSource
	}
	if e != nil {
		return s, e
	}
	e = tx.QueryRow(ctx, `SELECT id,version,moysklad_account,target_kind,target_id,ozon_offer_id,ozon_product_id,provenance,confirmed_at FROM product_mappings WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_product_id=$3 LIMIT 1 FOR SHARE`, in.Tenant, in.OzonAccount, in.ProductID).Scan(&s.MappingID, &s.MappingVersion, &s.MappingTargetAccount, &s.MappingKind, &s.MappingTarget, &s.MappingOffer, &s.MappingProduct, &s.MappingProvenance, &s.MappingConfirmed)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return s, e
	}
	if e == nil {
		s.Conflict = s.MappingProduct != in.ProductID || s.MappingTargetAccount != in.MoyskladAccount || s.MappingKind != in.TargetKind || s.MappingTarget != in.TargetID || s.MappingOffer != s.Offer
	}
	var otherConflict bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_mappings WHERE tenant_id=$1 AND ozon_account=$2 AND ozon_offer_id=$3 AND ozon_product_id<>$4)`, in.Tenant, in.OzonAccount, s.Offer, in.ProductID).Scan(&otherConflict)
	if e != nil {
		return s, e
	}
	s.Conflict = s.Conflict || otherConflict
	return s, nil
}
func snapshotHash(s reviewSnapshot) string {
	b, _ := json.Marshal([]any{s.OzonID, s.MoyID, s.Offer, s.SKU, s.Archived, s.Name, s.Article, s.Code, s.Parent, s.Updated})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func mappingSnapshotHash(s reviewSnapshot) string {
	b, _ := json.Marshal([]any{s.MappingID, s.MappingVersion, s.MappingTargetAccount, s.MappingKind, s.MappingTarget, s.MappingOffer, s.MappingProduct, s.MappingProvenance, s.MappingConfirmed, s.Conflict})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Store) CreateMappingReview(ctx context.Context, in domain.MappingReviewRequest) (domain.MappingReviewResult, error) {
	if (MatchScope{Tenant: in.Tenant, OzonAccount: in.OzonAccount, MoyskladAccount: in.MoyskladAccount}).Validate() != nil || in.ProductID == "" || in.TargetID == "" || (in.TargetKind != "product" && in.TargetKind != "variant") || in.SessionID == "" || in.Lifetime <= 0 || in.Lifetime > 10*time.Minute || (in.Evidence != domain.MappingOperatorAttested && in.Evidence != domain.MappingDeterministicCandidate) {
		return domain.MappingReviewResult{}, ErrReviewEvidence
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return domain.MappingReviewResult{}, e
	}
	defer tx.Rollback(context.Background())
	if e = lockReview(ctx, tx, MatchScope{Tenant: in.Tenant, OzonAccount: in.OzonAccount, MoyskladAccount: in.MoyskladAccount}); e != nil {
		return domain.MappingReviewResult{}, e
	}
	snap, e := snapshotReview(ctx, tx, in)
	if e != nil {
		return domain.MappingReviewResult{}, e
	}
	src := domain.MatchSource{Identity: domain.ProductIdentity{System: "ozon", Account: in.OzonAccount, Kind: "product", ID: in.ProductID}, OfferID: snap.Offer}
	target := domain.MatchTarget{Identity: domain.ProductIdentity{System: "moysklad", Account: in.MoyskladAccount, Kind: in.TargetKind, ID: in.TargetID}, Article: snap.Article, Code: snap.Code}
	if in.Evidence == domain.MappingDeterministicCandidate && len(matching.New([]domain.MatchTarget{target}, nil).Match(in.Tenant, src, in.MoyskladAccount).Candidates) == 0 {
		return domain.MappingReviewResult{}, ErrReviewEvidence
	}
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		return domain.MappingReviewResult{}, e
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	r := domain.MappingReview{ID: hex.EncodeToString(random[:]), Tenant: in.Tenant, Source: src.Identity, Target: target.Identity, OzonImportID: snap.OzonID, MoyskladImportID: snap.MoyID, ExistingMappingVersion: snap.MappingVersion, EvidenceClass: in.Evidence, CreatedAt: now, ExpiresAt: now.Add(in.Lifetime)}
	if e = r.Validate(); e != nil {
		return domain.MappingReviewResult{}, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO product_mapping_reviews(id,tenant_id,ozon_account,moysklad_account,ozon_product_id,target_kind,target_id,ozon_import_id,moysklad_import_id,existing_mapping_id,existing_mapping_version,observation_hash,mapping_hash,evidence_class,principal_type,session_audit_id,created_at,expires_at,ozon_import_fingerprint,moysklad_import_fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'LOCAL_OPERATOR_SESSION',$15,$16,$17,$18,$19)`, r.ID, r.Tenant, r.Source.Account, r.Target.Account, r.Source.ID, r.Target.Kind, r.Target.ID, r.OzonImportID, r.MoyskladImportID, snap.MappingID, snap.MappingVersion, snapshotHash(snap), mappingSnapshotHash(snap), r.EvidenceClass, in.SessionID, r.CreatedAt, r.ExpiresAt, snap.OzonImportFingerprint, snap.MoyImportFingerprint)
	if e != nil {
		return domain.MappingReviewResult{}, fmt.Errorf("persist review: %w", e)
	}
	if e = tx.Commit(ctx); e != nil {
		return domain.MappingReviewResult{}, e
	}
	return domain.MappingReviewResult{Review: r, Status: "CURRENT", MappingConflict: snap.Conflict, PrincipalType: "LOCAL_OPERATOR_SESSION"}, nil
}

func (s *Store) InspectMappingReview(ctx context.Context, id, sessionID string) (domain.MappingReviewResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return domain.MappingReviewResult{}, e
	}
	defer tx.Rollback(context.Background())
	stored, e := loadMappingReview(ctx, tx, id, sessionID)
	if e != nil {
		return domain.MappingReviewResult{}, e
	}
	if e = lockReview(ctx, tx, stored.scope()); e != nil {
		return domain.MappingReviewResult{}, e
	}
	status, snap, e := evaluateMappingReview(ctx, tx, stored)
	if e != nil {
		return domain.MappingReviewResult{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return domain.MappingReviewResult{}, e
	}
	return domain.MappingReviewResult{Review: stored.review, Status: status, MappingConflict: snap.Conflict, PrincipalType: "LOCAL_OPERATOR_SESSION"}, nil
}

type storedMappingReview struct {
	review                                      domain.MappingReview
	mappingID                                   *int64
	observationHash, mappingHash                string
	ozonImportFingerprint, moyImportFingerprint *string
}

func (s storedMappingReview) scope() MatchScope {
	return MatchScope{Tenant: s.review.Tenant, OzonAccount: s.review.Source.Account, MoyskladAccount: s.review.Target.Account}
}

// loadMappingReview enforces the creating session before returning any scope.
func loadMappingReview(ctx context.Context, tx pgx.Tx, id, sessionID string) (storedMappingReview, error) {
	var s storedMappingReview
	r := &s.review
	var evidence string
	err := tx.QueryRow(ctx, `SELECT tenant_id,ozon_account,moysklad_account,ozon_product_id,target_kind,target_id,ozon_import_id,moysklad_import_id,existing_mapping_id,existing_mapping_version,observation_hash,mapping_hash,evidence_class,created_at,expires_at,ozon_import_fingerprint,moysklad_import_fingerprint FROM product_mapping_reviews WHERE id=$1 AND session_audit_id=$2`, id, sessionID).Scan(&r.Tenant, &r.Source.Account, &r.Target.Account, &r.Source.ID, &r.Target.Kind, &r.Target.ID, &r.OzonImportID, &r.MoyskladImportID, &s.mappingID, &r.ExistingMappingVersion, &s.observationHash, &s.mappingHash, &evidence, &r.CreatedAt, &r.ExpiresAt, &s.ozonImportFingerprint, &s.moyImportFingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrReviewMissing
	}
	if err != nil {
		return s, err
	}
	r.ID = id
	r.Source.System, r.Source.Kind = "ozon", "product"
	r.Target.System = "moysklad"
	r.EvidenceClass = domain.MappingEvidenceClass(evidence)
	return s, nil
}

// evaluateMappingReview must run while the caller holds the review's existing
// importer and matching locks; it reuses the same revision policy for reads and writes.
func evaluateMappingReview(ctx context.Context, tx pgx.Tx, stored storedMappingReview) (string, reviewSnapshot, error) {
	r := stored.review
	in := domain.MappingReviewRequest{Tenant: r.Tenant, OzonAccount: r.Source.Account, MoyskladAccount: r.Target.Account, ProductID: r.Source.ID, TargetKind: r.Target.Kind, TargetID: r.Target.ID, Evidence: r.EvidenceClass}
	snap, err := snapshotReview(ctx, tx, in)
	status := "CURRENT"
	if errors.Is(err, ErrReviewSource) {
		status = "STALE_SOURCE"
	} else if err != nil {
		return "", snap, err
	} else if snap.OzonID != r.OzonImportID || snap.MoyID != r.MoyskladImportID || snapshotHash(snap) != stored.observationHash || stored.ozonImportFingerprint == nil || stored.moyImportFingerprint == nil || *stored.ozonImportFingerprint != snap.OzonImportFingerprint || *stored.moyImportFingerprint != snap.MoyImportFingerprint {
		status = "STALE_SOURCE"
	}
	if err == nil && status == "CURRENT" && (pointerInt64Changed(stored.mappingID, snap.MappingID) || pointerIntChanged(r.ExistingMappingVersion, snap.MappingVersion) || stored.mappingHash != mappingSnapshotHash(snap)) {
		status = "STALE_MAPPING"
	}
	if !time.Now().Before(r.ExpiresAt) {
		status = "EXPIRED"
	}
	return status, snap, nil
}

func pointerInt64Changed(a, b *int64) bool {
	if a == nil || b == nil {
		return a != nil || b != nil
	}
	return *a != *b
}
func pointerIntChanged(a, b *int) bool {
	if a == nil || b == nil {
		return a != nil || b != nil
	}
	return *a != *b
}
