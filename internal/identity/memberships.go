package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SetMembership accepts an empty role to revoke an existing membership. A
// workspace row lock serializes owner changes so the last owner cannot be lost.
func (s *Store) SetMembership(ctx context.Context, session Session, workspace, email, role string) error {
	email, err := canonicalEmail(email)
	if err != nil || (role != "" && !ValidRole(role)) {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start membership change: %w", err)
	}
	defer rollback(tx)
	if err = lockWorkspace(ctx, tx, workspace); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT saas_require_actor($1,$2,true)`, workspace, session.AuditID); err != nil {
		return ErrForbidden
	}
	if err = changeMembership(ctx, tx, session, workspace, email, role); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockWorkspace(ctx context.Context, tx pgx.Tx, workspace string) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM saas_workspaces WHERE id=$1 AND NOT legacy_unclaimed FOR UPDATE`, workspace).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("lock workspace: %w", err)
	}
	return nil
}

func changeMembership(ctx context.Context, tx pgx.Tx, session Session, workspace, email, desired string) error {
	var actorRole string
	err := tx.QueryRow(ctx, `SELECT role FROM saas_memberships WHERE workspace_id=$1 AND user_id=$2 AND revoked_at IS NULL`, workspace, session.User.ID).Scan(&actorRole)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !CanAdmin(actorRole)) {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("read acting membership: %w", err)
	}
	var targetID, oldRole string
	err = tx.QueryRow(ctx, `SELECT u.id,COALESCE(m.role,'') FROM saas_users u LEFT JOIN saas_memberships m
	 ON m.workspace_id=$1 AND m.user_id=u.id AND m.revoked_at IS NULL WHERE u.email=$2 AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL`, workspace, email).Scan(&targetID, &oldRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("read target membership: %w", err)
	}
	if actorRole != "OWNER" && (oldRole == "OWNER" || desired == "OWNER") {
		return ErrForbidden
	}
	if err = ensureOwner(ctx, tx, workspace, oldRole, desired); err != nil {
		return err
	}
	return persistMembership(ctx, tx, session, workspace, targetID, oldRole, desired)
}

func ensureOwner(ctx context.Context, tx pgx.Tx, workspace, oldRole, desired string) error {
	if oldRole != "OWNER" || desired == "OWNER" {
		return nil
	}
	var owners int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM saas_memberships WHERE workspace_id=$1 AND role='OWNER' AND revoked_at IS NULL`, workspace).Scan(&owners); err != nil {
		return fmt.Errorf("count owners: %w", err)
	}
	if owners < 2 {
		return ErrLastOwner
	}
	return nil
}

func persistMembership(ctx context.Context, tx pgx.Tx, session Session, workspace, targetID, oldRole, desired string) error {
	if desired == "" {
		if oldRole == "" {
			return ErrForbidden
		}
		if _, err := tx.Exec(ctx, `UPDATE saas_memberships SET revoked_at=clock_timestamp() WHERE workspace_id=$1 AND user_id=$2`, workspace, targetID); err != nil {
			return fmt.Errorf("revoke membership: %w", err)
		}
		return event(ctx, tx, workspace, session.User.ID, session.AuditID, "MEMBER_REMOVED", targetID)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_memberships(workspace_id,user_id,role) VALUES($1,$2,$3)
	 ON CONFLICT(workspace_id,user_id) DO UPDATE SET role=EXCLUDED.role,revoked_at=NULL`, workspace, targetID, desired); err != nil {
		return fmt.Errorf("set membership: %w", err)
	}
	return event(ctx, tx, workspace, session.User.ID, session.AuditID, "MEMBER_ROLE_CHANGED", targetID+":"+desired)
}

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
