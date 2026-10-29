package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// BindLegacyMerchant is for explicit local migration only. It never moves
// provider data or changes its tenant identifier, including encryption AAD.
func (s *Store) BindLegacyMerchant(ctx context.Context, userID, tenant string) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start merchant migration: %w", err)
	}
	defer rollback(tx)
	if err = lockMigrationOwner(ctx, tx, userID, tenant); err != nil {
		return err
	}
	if err = checkLegacyMappings(ctx, tx, tenant); err != nil {
		return err
	}
	if err = checkLegacyPublicationScopes(ctx, tx, tenant); err != nil {
		return err
	}
	if err = bindMigratedTenant(ctx, tx, userID, tenant); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockMigrationOwner(ctx context.Context, tx pgx.Tx, userID, tenant string) error {
	var verified bool
	err := tx.QueryRow(ctx, `SELECT u.email_verified_at IS NOT NULL AND u.disabled_at IS NULL
	 FROM saas_users u JOIN saas_memberships m ON m.user_id=u.id
	 JOIN saas_workspaces w ON w.id=m.workspace_id
	 WHERE u.id=$1 AND w.id=$2 AND m.role='OWNER' AND m.revoked_at IS NULL AND NOT w.legacy_unclaimed
	 AND 1=(SELECT count(*) FROM saas_memberships o WHERE o.workspace_id=w.id AND o.role='OWNER' AND o.revoked_at IS NULL)
	 FOR UPDATE OF u,m,w`, userID, tenant).Scan(&verified)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !verified) {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("verify migration owner: %w", err)
	}
	return nil
}

func bindMigratedTenant(ctx context.Context, tx pgx.Tx, userID, tenant string) error {
	var previous string
	err := tx.QueryRow(ctx, `SELECT tenant_id FROM saas_merchant_accounts WHERE user_id=$1 FOR UPDATE`, userID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lock merchant binding: %w", err)
	}
	if previous == tenant {
		return nil
	}
	if previous != "" {
		empty, err := tenantHasNoBusinessRecords(ctx, tx, previous)
		if err != nil {
			return err
		}
		if !empty {
			return errors.New("existing merchant account contains business records; migration requires manual resolution")
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO saas_merchant_accounts(user_id,tenant_id) VALUES($1,$2)
	 ON CONFLICT(user_id) DO UPDATE SET tenant_id=EXCLUDED.tenant_id`, userID, tenant); err != nil {
		return fmt.Errorf("bind migrated merchant account: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL`, userID); err != nil {
		return fmt.Errorf("revoke sessions for migration: %w", err)
	}
	return event(ctx, tx, tenant, userID, "", "LOCAL_MERCHANT_MIGRATION", "")
}

// The automatically created empty account may be replaced only if no table
// containing tenant-scoped business data refers to it. Identity history stays.
func tenantHasNoBusinessRecords(ctx context.Context, tx pgx.Tx, tenant string) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT table_schema,table_name FROM information_schema.columns
	 WHERE table_schema=current_schema() AND column_name='tenant_id' AND table_name<>'saas_merchant_accounts'`)
	if err != nil {
		return false, fmt.Errorf("inspect tenant tables: %w", err)
	}
	var tables []pgx.Identifier
	for rows.Next() {
		var schema, table string
		if err = rows.Scan(&schema, &table); err != nil {
			rows.Close()
			return false, fmt.Errorf("inspect tenant table: %w", err)
		}
		tables = append(tables, pgx.Identifier{schema, table})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, fmt.Errorf("inspect tenant tables: %w", err)
	}
	for _, table := range tables {
		var present bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+table.Sanitize()+` WHERE tenant_id=$1)`, tenant).Scan(&present); err != nil {
			return false, fmt.Errorf("check tenant records: %w", err)
		}
		if present {
			return false, nil
		}
	}
	return true, nil
}
