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

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
)

type warehouseSnapshot struct {
	MoyID, OzonID                                       int64
	MoyFingerprint, OzonFingerprint                     string
	MoyName, MoyCode, MoyArticle, MoyParent, MoyUpdated string
	OzonName, OzonStatus, OzonType                      string
	IsRFBS                                              *bool
	MappingID                                           *int64
	MappingVersion                                      *int
	MappingProvenance                                   *string
	MappingConfirmed                                    *time.Time
	Conflict                                            bool
}

func warehouseHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func warehouseObservationHash(s warehouseSnapshot) string {
	return warehouseHash([]any{s.MoyName, s.MoyCode, s.MoyArticle, s.MoyParent, s.MoyUpdated, s.OzonName, s.OzonStatus, s.OzonType, s.IsRFBS})
}
func warehouseMappingHash(s warehouseSnapshot) string {
	return warehouseHash([]any{s.MappingID, s.MappingVersion, s.MappingProvenance, s.MappingConfirmed, s.Conflict})
}
func lockWarehouseReview(ctx context.Context, tx pgx.Tx, scope MatchScope) error {
	if err := lockReview(ctx, tx, scope); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "warehouse-mapping:"+scope.Tenant)
	return err
}
func warehouseReviewSnapshot(ctx context.Context, tx pgx.Tx, in domain.WarehouseReviewRequest) (warehouseSnapshot, error) {
	var s warehouseSnapshot
	for _, v := range []struct{ source, account string }{{"ozon", in.OzonAccount}, {"moysklad", in.MoyskladAccount}} {
		var ignored string
		err := tx.QueryRow(ctx, `SELECT account_id FROM stock_connections WHERE tenant_id=$1 AND source=$2 AND account_id=$3 FOR SHARE`, in.Tenant, v.source, v.account).Scan(&ignored)
		if errors.Is(err, pgx.ErrNoRows) {
			return s, ErrReviewScope
		}
		if err != nil {
			return s, err
		}
	}
	err := tx.QueryRow(ctx, `SELECT i.id,i.fingerprint FROM source_imports i JOIN ozon_import_batches b ON b.import_id=i.id AND b.traversal_status='completed' JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source='ozon' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1 FOR SHARE OF i`, in.Tenant, in.OzonAccount).Scan(&s.OzonID, &s.OzonFingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrReviewSource
	}
	if err != nil {
		return s, err
	}
	err = tx.QueryRow(ctx, `SELECT i.id,i.fingerprint FROM source_imports i JOIN stock_connections c ON c.tenant_id=$1 AND c.source=i.source AND c.account_id=i.account_id WHERE i.source='moysklad' AND i.account_id=$2 AND (c.include_import_history OR i.started_at>=c.registered_at) ORDER BY i.id DESC LIMIT 1 FOR SHARE OF i`, in.Tenant, in.MoyskladAccount).Scan(&s.MoyID, &s.MoyFingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrReviewSource
	}
	if err != nil {
		return s, err
	}
	err = tx.QueryRow(ctx, `SELECT name,code,article,parent_id,source_updated FROM source_entities WHERE import_id=$1 AND kind='store' AND external_id=$2 FOR SHARE`, s.MoyID, in.MoyskladWarehouseID).Scan(&s.MoyName, &s.MoyCode, &s.MoyArticle, &s.MoyParent, &s.MoyUpdated)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrReviewSource
	}
	if err != nil {
		return s, err
	}
	err = tx.QueryRow(ctx, `SELECT name,status,warehouse_type,is_rfbs FROM ozon_warehouse_observations WHERE import_id=$1 AND warehouse_id=$2 FOR SHARE`, s.OzonID, in.OzonWarehouseID).Scan(&s.OzonName, &s.OzonStatus, &s.OzonType, &s.IsRFBS)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrReviewSource
	}
	if err != nil {
		return s, err
	}
	rows, err := tx.Query(ctx, `SELECT id,version,moysklad_warehouse_id,ozon_warehouse_id,provenance,confirmed_at FROM warehouse_mappings WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND status='active' AND (moysklad_warehouse_id=$4 OR ozon_warehouse_id=$5) ORDER BY id FOR SHARE`, in.Tenant, in.MoyskladAccount, in.OzonAccount, in.MoyskladWarehouseID, in.OzonWarehouseID)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var id int64
		var version int
		var m, o, p string
		var confirmed time.Time
		if err = rows.Scan(&id, &version, &m, &o, &p, &confirmed); err != nil {
			rows.Close()
			return s, err
		}
		if m != in.MoyskladWarehouseID || o != in.OzonWarehouseID {
			s.Conflict = true
		} else {
			s.MappingID = &id
			s.MappingVersion = &version
			s.MappingProvenance = &p
			s.MappingConfirmed = &confirmed
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, err
	}
	// Existing mapping history must be visible under both account registrations.
	if s.MappingID != nil {
		var visible bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouse_mapping_history h JOIN stock_connections mc ON mc.tenant_id=h.tenant_id AND mc.source='moysklad' AND mc.account_id=h.moysklad_account JOIN stock_connections oc ON oc.tenant_id=h.tenant_id AND oc.source='ozon' AND oc.account_id=h.ozon_account JOIN source_imports mi ON mi.id=h.moysklad_import_id AND mi.source='moysklad' AND mi.account_id=h.moysklad_account JOIN source_imports oi ON oi.id=h.ozon_import_id AND oi.source='ozon' AND oi.account_id=h.ozon_account WHERE h.mapping_id=$1 AND h.version=$2 AND h.tenant_id=$3 AND h.moysklad_account=$4 AND h.ozon_account=$5 AND h.moysklad_warehouse_id=$6 AND h.ozon_warehouse_id=$7 AND h.status='active' AND h.provenance=$8 AND h.confirmed_at=$9 AND (mc.include_import_history OR (mi.started_at>=mc.registered_at AND h.confirmed_at>=mc.registered_at)) AND (oc.include_import_history OR (oi.started_at>=oc.registered_at AND h.confirmed_at>=oc.registered_at)))`, s.MappingID, s.MappingVersion, in.Tenant, in.MoyskladAccount, in.OzonAccount, in.MoyskladWarehouseID, in.OzonWarehouseID, s.MappingProvenance, s.MappingConfirmed).Scan(&visible)
		if err != nil {
			return s, err
		}
		if !visible {
			s.Conflict = true
		}
	}
	return s, nil
}
func (s *Store) CreateWarehouseReview(ctx context.Context, in domain.WarehouseReviewRequest) (domain.WarehouseReviewResult, error) {
	scope := MatchScope{Tenant: in.Tenant, MoyskladAccount: in.MoyskladAccount, OzonAccount: in.OzonAccount}
	if in.Validate() != nil {
		return domain.WarehouseReviewResult{}, ErrReviewEvidence
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.WarehouseReviewResult{}, err
	}
	defer tx.Rollback(context.Background())
	if err = lockWarehouseReview(ctx, tx, scope); err != nil {
		return domain.WarehouseReviewResult{}, err
	}
	snap, err := warehouseReviewSnapshot(ctx, tx, in)
	if err != nil {
		return domain.WarehouseReviewResult{}, err
	}
	if snap.Conflict {
		return domain.WarehouseReviewResult{}, ErrReviewConflict
	}
	if snap.IsRFBS == nil || *snap.IsRFBS {
		return domain.WarehouseReviewResult{}, ErrReviewEvidence
	}
	var b [16]byte
	if _, err = rand.Read(b[:]); err != nil {
		return domain.WarehouseReviewResult{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	r := domain.WarehouseReview{ID: hex.EncodeToString(b[:]), Tenant: in.Tenant, MoyskladAccount: in.MoyskladAccount, OzonAccount: in.OzonAccount, MoyskladWarehouseID: in.MoyskladWarehouseID, OzonWarehouseID: in.OzonWarehouseID, MoyskladImportID: snap.MoyID, OzonImportID: snap.OzonID, ExistingMappingID: snap.MappingID, ExistingMappingVersion: snap.MappingVersion, CreatedAt: now, ExpiresAt: now.Add(in.Lifetime)}
	_, err = tx.Exec(ctx, `INSERT INTO warehouse_mapping_reviews(id,tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,moysklad_import_id,ozon_import_id,moysklad_import_fingerprint,ozon_import_fingerprint,observation_hash,mapping_hash,existing_mapping_id,existing_mapping_version,principal_type,session_audit_id,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$18,$15,$16,$17)`, r.ID, r.Tenant, r.MoyskladAccount, r.OzonAccount, r.MoyskladWarehouseID, r.OzonWarehouseID, r.MoyskladImportID, r.OzonImportID, snap.MoyFingerprint, snap.OzonFingerprint, warehouseObservationHash(snap), warehouseMappingHash(snap), r.ExistingMappingID, r.ExistingMappingVersion, in.SessionID, r.CreatedAt, r.ExpiresAt, auditPrincipal(in.SessionID))
	if err != nil {
		return domain.WarehouseReviewResult{}, fmt.Errorf("persist warehouse review: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.WarehouseReviewResult{}, err
	}
	status := "CURRENT"
	if !time.Now().Before(r.ExpiresAt) {
		status = "EXPIRED"
	}
	return domain.WarehouseReviewResult{Review: r, Status: status, PrincipalType: auditPrincipal(in.SessionID)}, nil
}
func (s *Store) InspectWarehouseReview(ctx context.Context, id, sessionID string) (domain.WarehouseReviewResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return domain.WarehouseReviewResult{}, err
	}
	defer tx.Rollback(context.Background())
	stored, err := loadWarehouseReview(ctx, tx, id, sessionID)
	if err != nil {
		return domain.WarehouseReviewResult{}, err
	}
	if err = lockWarehouseReview(ctx, tx, stored.scope()); err != nil {
		return domain.WarehouseReviewResult{}, err
	}
	status, _, err := evaluateWarehouseReview(ctx, tx, stored)
	if err != nil {
		return domain.WarehouseReviewResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.WarehouseReviewResult{}, err
	}
	return domain.WarehouseReviewResult{Review: stored.review, Status: status, PrincipalType: auditPrincipal(sessionID)}, nil
}

type storedWarehouseReview struct {
	review                                                        domain.WarehouseReview
	moyFingerprint, ozonFingerprint, observationHash, mappingHash string
}

func (s storedWarehouseReview) scope() MatchScope {
	return MatchScope{Tenant: s.review.Tenant, MoyskladAccount: s.review.MoyskladAccount, OzonAccount: s.review.OzonAccount}
}

// Load only records owned by the authenticating session, before disclosing scope.
func loadWarehouseReview(ctx context.Context, tx pgx.Tx, id, sessionID string) (storedWarehouseReview, error) {
	var s storedWarehouseReview
	r := &s.review
	err := tx.QueryRow(ctx, `SELECT id,tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,moysklad_import_id,ozon_import_id,moysklad_import_fingerprint,ozon_import_fingerprint,observation_hash,mapping_hash,existing_mapping_id,existing_mapping_version,created_at,expires_at FROM warehouse_mapping_reviews WHERE id=$1 AND session_audit_id=$2`, id, sessionID).Scan(&r.ID, &r.Tenant, &r.MoyskladAccount, &r.OzonAccount, &r.MoyskladWarehouseID, &r.OzonWarehouseID, &r.MoyskladImportID, &r.OzonImportID, &s.moyFingerprint, &s.ozonFingerprint, &s.observationHash, &s.mappingHash, &r.ExistingMappingID, &r.ExistingMappingVersion, &r.CreatedAt, &r.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrReviewMissing
	}
	return s, err
}

// Called under the same importer and warehouse-mapping locks for inspection and confirmation.
func evaluateWarehouseReview(ctx context.Context, tx pgx.Tx, stored storedWarehouseReview) (string, warehouseSnapshot, error) {
	r := stored.review
	in := domain.WarehouseReviewRequest{Tenant: r.Tenant, MoyskladAccount: r.MoyskladAccount, OzonAccount: r.OzonAccount, MoyskladWarehouseID: r.MoyskladWarehouseID, OzonWarehouseID: r.OzonWarehouseID}
	snap, err := warehouseReviewSnapshot(ctx, tx, in)
	status := "CURRENT"
	if errors.Is(err, ErrReviewSource) || errors.Is(err, ErrReviewScope) {
		status = "STALE_SOURCE"
	} else if err != nil {
		return "", snap, err
	} else if snap.MoyID != r.MoyskladImportID || snap.OzonID != r.OzonImportID || snap.MoyFingerprint != stored.moyFingerprint || snap.OzonFingerprint != stored.ozonFingerprint || warehouseObservationHash(snap) != stored.observationHash || snap.IsRFBS == nil || *snap.IsRFBS {
		status = "STALE_SOURCE"
	} else if snap.Conflict || warehouseMappingHash(snap) != stored.mappingHash || pointerInt64Changed(r.ExistingMappingID, snap.MappingID) || pointerIntChanged(r.ExistingMappingVersion, snap.MappingVersion) {
		status = "STALE_MAPPING"
	}
	if !time.Now().Before(r.ExpiresAt) {
		status = "EXPIRED"
	}
	return status, snap, nil
}
