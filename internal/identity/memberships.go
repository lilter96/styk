package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ClaimLegacy is called only by an explicitly enabled local administrative
// command; no HTTP route is provided for claiming existing data.
func (s *Store) ClaimLegacy(ctx context.Context, workspace, userID string) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start legacy claim: %w", err)
	}
	defer rollback(tx)
	var claimable bool
	err = tx.QueryRow(ctx, `SELECT legacy_unclaimed AND NOT EXISTS(SELECT 1 FROM saas_memberships WHERE workspace_id=$1)
	 FROM saas_workspaces WHERE id=$1 FOR UPDATE`, workspace).Scan(&claimable)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !claimable) {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("lock legacy workspace: %w", err)
	}
	if err = checkLegacyMappings(ctx, tx, workspace); err != nil {
		return err
	}
	if err = checkLegacyPublicationScopes(ctx, tx, workspace); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO saas_memberships(workspace_id,user_id,role) SELECT $1,id,'OWNER' FROM saas_users WHERE id=$2 AND disabled_at IS NULL AND email_verified_at IS NOT NULL`, workspace, userID); err != nil {
		return fmt.Errorf("assign legacy owner: %w", err)
	}
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_memberships WHERE workspace_id=$1 AND user_id=$2 AND role='OWNER')`, workspace, userID).Scan(&owned); err != nil {
		return fmt.Errorf("verify legacy owner: %w", err)
	}
	if !owned {
		return ErrForbidden
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_workspaces SET legacy_unclaimed=false WHERE id=$1`, workspace); err != nil {
		return fmt.Errorf("claim legacy workspace: %w", err)
	}
	if err = event(ctx, tx, workspace, userID, "", "LOCAL_BOOTSTRAP_CLAIM", ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// New links are constrained by migration 38. Before exposing preserved legacy
// history, separately reject any pre-existing cross-workspace association.
func checkLegacyPublicationScopes(ctx context.Context, tx pgx.Tx, workspace string) error {
	var invalid bool
	err := tx.QueryRow(ctx, `SELECT
	 EXISTS(SELECT 1 FROM stock_sync_settings s JOIN stock_sync_reviews r ON r.id=s.review_id WHERE s.tenant_id=$1 AND
	  (s.tenant_id,s.moysklad_account,s.ozon_account) IS DISTINCT FROM (r.tenant_id,r.moysklad_account,r.ozon_account))
	 OR EXISTS(SELECT 1 FROM stock_publications p JOIN stock_sync_reviews r ON r.id=p.review_id WHERE p.tenant_id=$1 AND
	  (p.tenant_id,p.moysklad_account,p.ozon_account) IS DISTINCT FROM (r.tenant_id,r.moysklad_account,r.ozon_account))
	 OR EXISTS(SELECT 1 FROM stock_publications p JOIN stock_publication_targets t ON t.operation_id=p.id WHERE p.tenant_id=$1 AND t.ozon_account<>p.ozon_account)
	 OR EXISTS(SELECT 1 FROM stock_sync_mutations m JOIN stock_publications p ON p.id=m.operation_id WHERE m.tenant_id=$1 AND
	  (m.tenant_id,m.moysklad_account,m.ozon_account) IS DISTINCT FROM (p.tenant_id,p.moysklad_account,p.ozon_account))
	 OR EXISTS(SELECT 1 FROM stock_initial_publications i JOIN stock_publications p ON p.id=i.publication_id WHERE i.tenant_id=$1 AND
	  (i.tenant_id,i.moysklad_account,i.ozon_account) IS DISTINCT FROM (p.tenant_id,p.moysklad_account,p.ozon_account))
	 OR EXISTS(SELECT 1 FROM stock_auto_cycles c WHERE c.tenant_id=$1 AND (
	  (c.moysklad_import_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM ozon_import_operations o WHERE o.id=c.moysklad_import_id AND o.tenant_id=c.tenant_id AND o.provider='moysklad' AND o.account_id=c.moysklad_account)) OR
	  (c.ozon_import_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM ozon_import_operations o WHERE o.id=c.ozon_import_id AND o.tenant_id=c.tenant_id AND o.provider='ozon' AND o.account_id=c.ozon_account)) OR
	  (c.publication_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM stock_publications p WHERE p.id=c.publication_id AND p.tenant_id=c.tenant_id AND p.moysklad_account=c.moysklad_account AND p.ozon_account=c.ozon_account))))
	 OR EXISTS(SELECT 1 FROM stock_auto_cycle_publications l JOIN stock_auto_cycles c ON c.id=l.cycle_id JOIN stock_publications p ON p.id=l.publication_id WHERE c.tenant_id=$1 AND
	  ((p.tenant_id,p.moysklad_account,p.ozon_account) IS DISTINCT FROM (c.tenant_id,c.moysklad_account,c.ozon_account) OR NOT EXISTS
	   (SELECT 1 FROM stock_publication_targets t WHERE t.operation_id=p.id AND t.ozon_account=p.ozon_account AND t.product_id=l.product_id AND t.warehouse_id=l.warehouse_id)))`, workspace).Scan(&invalid)
	if err != nil {
		return fmt.Errorf("check legacy publication ownership: %w", err)
	}
	if invalid {
		return errors.New("legacy publication links require account ownership review before claim")
	}
	return nil
}

func checkLegacyMappings(ctx context.Context, tx pgx.Tx, workspace string) error {
	var invalid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_mappings p WHERE p.tenant_id=$1 AND
	 (NOT EXISTS(SELECT 1 FROM stock_connections c WHERE c.tenant_id=p.tenant_id AND c.source='ozon' AND c.account_id=p.ozon_account)
	 OR NOT EXISTS(SELECT 1 FROM stock_connections c WHERE c.tenant_id=p.tenant_id AND c.source='moysklad' AND c.account_id=p.moysklad_account)))
	 OR EXISTS(SELECT 1 FROM warehouse_mappings w WHERE w.tenant_id=$1 AND
	 (NOT EXISTS(SELECT 1 FROM stock_connections c WHERE c.tenant_id=w.tenant_id AND c.source='ozon' AND c.account_id=w.ozon_account)
	 OR NOT EXISTS(SELECT 1 FROM stock_connections c WHERE c.tenant_id=w.tenant_id AND c.source='moysklad' AND c.account_id=w.moysklad_account)))
	 OR EXISTS(SELECT 1 FROM product_mapping_history h WHERE h.tenant_id=$1 AND
	 (NOT EXISTS(SELECT 1 FROM stock_connections c WHERE c.tenant_id=h.tenant_id AND c.source='ozon' AND c.account_id=h.ozon_account)
	 OR NOT EXISTS(SELECT 1 FROM stock_connections c WHERE c.tenant_id=h.tenant_id AND c.source='moysklad' AND c.account_id=h.moysklad_account)
	 OR NOT EXISTS(SELECT 1 FROM source_imports i WHERE i.id=h.ozon_import_id AND i.source='ozon' AND i.account_id=h.ozon_account)
	 OR NOT EXISTS(SELECT 1 FROM source_imports i WHERE i.id=h.moysklad_import_id AND i.source='moysklad' AND i.account_id=h.moysklad_account)))
	 OR EXISTS(SELECT 1 FROM warehouse_mapping_history h WHERE h.tenant_id=$1 AND
	 (NOT EXISTS(SELECT 1 FROM stock_connections c WHERE c.tenant_id=h.tenant_id AND c.source='ozon' AND c.account_id=h.ozon_account)
	 OR NOT EXISTS(SELECT 1 FROM stock_connections c WHERE c.tenant_id=h.tenant_id AND c.source='moysklad' AND c.account_id=h.moysklad_account)
	 OR NOT EXISTS(SELECT 1 FROM source_imports i WHERE i.id=h.ozon_import_id AND i.source='ozon' AND i.account_id=h.ozon_account)
	 OR NOT EXISTS(SELECT 1 FROM source_imports i WHERE i.id=h.moysklad_import_id AND i.source='moysklad' AND i.account_id=h.moysklad_account)))
	 OR EXISTS(SELECT 1 FROM ozon_posting_batches b WHERE b.tenant_id=$1 AND NOT EXISTS
	 (SELECT 1 FROM stock_connections c WHERE c.tenant_id=b.tenant_id AND c.source='ozon' AND c.account_id=b.account_id))
	 OR EXISTS(SELECT 1 FROM ozon_posting_identities i WHERE i.tenant_id=$1 AND NOT EXISTS
	 (SELECT 1 FROM stock_connections c WHERE c.tenant_id=i.tenant_id AND c.source='ozon' AND c.account_id=i.account_id))`, workspace).Scan(&invalid)
	if err != nil {
		return fmt.Errorf("check legacy mapping ownership: %w", err)
	}
	if invalid {
		return errors.New("legacy mappings require account ownership review before claim")
	}
	return nil
}
