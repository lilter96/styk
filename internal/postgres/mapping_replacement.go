package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
)

func replacementAllowed(s reviewSnapshot, r domain.MappingReview) bool {
	return s.MappingID != nil && s.MappingVersion != nil && r.ExistingMappingVersion != nil &&
		*s.MappingVersion == *r.ExistingMappingVersion && s.MappingTargetAccount == r.Target.Account &&
		!s.OfferConflict && (s.MappingKind != r.Target.Kind || s.MappingTarget != r.Target.ID || s.MappingOffer != s.Offer)
}

// Rare operator replacement coordinates with durable work admission/dispatch.
// The table locks are bounded by Store.Timeout and held only for this local
// transaction. Active or unknown work blocks replacement; it is never cancelled.
func replacementWorkIdle(ctx context.Context, tx pgx.Tx, scope MatchScope, mappingID int64, productID string) error {
	if _, err := tx.Exec(ctx, `LOCK TABLE catalog_sync_work, orders_work, stock_publication_targets IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("coordinate mapping replacement: %w", err)
	}
	var busy bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM catalog_sync_work w JOIN catalog_sync_enrollments e
 ON (e.tenant_id,e.moysklad_account,e.ozon_account,e.config_revision,e.target_key)=(w.tenant_id,w.moysklad_account,w.ozon_account,w.config_revision,w.target_key)
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3
 AND e.enrollment->>'mapping_id'=$4::bigint::text AND w.state NOT IN ('RECONCILED','REJECTED','BLOCKED'))
 OR EXISTS(SELECT 1 FROM orders_work w JOIN orders_work_mappings m ON m.work_id=w.id
 WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND m.mapping_id=$4
 AND w.state NOT IN ('RECONCILED','REJECTED','BLOCKED'))
 OR EXISTS(SELECT 1 FROM stock_publication_targets t JOIN stock_publications p ON p.id=t.operation_id
 WHERE p.tenant_id=$1 AND p.moysklad_account=$2 AND p.ozon_account=$3 AND t.product_id=$5
 AND t.state IN ('QUEUED','DISPATCHING','ACKNOWLEDGED','UNKNOWN'))`, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, mappingID, productID).Scan(&busy)
	if err != nil {
		return fmt.Errorf("read active mapping work: %w", err)
	}
	if busy {
		return ErrMappingBusy
	}
	return nil
}

func writeReviewedMapping(ctx context.Context, tx pgx.Tx, in mappingInsert, snap reviewSnapshot, req domain.MappingConfirmationRequest) (persistedMapping, error) {
	if !req.ReplaceExisting {
		return insertConfirmedMapping(ctx, tx, in)
	}
	var m persistedMapping
	m.ID, m.Version = *snap.MappingID, *snap.MappingVersion+1
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&m.ConfirmedAt); err != nil {
		return m, fmt.Errorf("read replacement clock: %w", err)
	}
	_, err := tx.Exec(ctx, `INSERT INTO product_mapping_replacements(mapping_id,previous_version,mapping_version,review_id,session_audit_id) VALUES($1,$2,$3,$4,$5)`, m.ID, *snap.MappingVersion, m.Version, req.ReviewID, req.SessionID)
	if err != nil {
		return m, fmt.Errorf("record mapping replacement: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE product_mappings SET version=$2,ozon_offer_id=$3,target_kind=$4,target_id=$5,provenance=$6,confirmed_at=$7 WHERE id=$1 AND version=$8 AND tenant_id=$9 AND moysklad_account=$10 AND ozon_account=$11 AND ozon_product_id=$12`, m.ID, m.Version, in.OfferID, in.TargetKind, in.TargetID, in.Provenance, m.ConfirmedAt, *snap.MappingVersion, in.Scope.Tenant, in.Scope.MoyskladAccount, in.Scope.OzonAccount, in.ProductID)
	if err != nil {
		return m, fmt.Errorf("replace reviewed mapping: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return m, ErrReviewStale
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_mapping_history(mapping_id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,ozon_import_id,moysklad_import_id,confirmation_review_id) SELECT id,version,tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,provenance,confirmed_at,$2,$3,$4 FROM product_mappings WHERE id=$1`, m.ID, in.OzonImportID, in.MoyskladImportID, in.ReviewID)
	if err != nil {
		return m, fmt.Errorf("record replacement history: %w", err)
	}
	return m, nil
}
