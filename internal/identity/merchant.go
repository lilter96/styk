package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// MerchantTenant resolves the sole account from the authenticated user. Old
// memberships do not grant access to a second account.
func (s *Store) MerchantTenant(ctx context.Context, userID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var tenant string
	err := s.pool.QueryRow(ctx, `SELECT a.tenant_id FROM saas_merchant_accounts a
	 JOIN saas_memberships m ON (m.workspace_id,m.user_id)=(a.tenant_id,a.user_id)
	 JOIN saas_workspaces w ON w.id=a.tenant_id JOIN saas_users u ON u.id=a.user_id
	 WHERE a.user_id=$1 AND m.role='OWNER' AND m.revoked_at IS NULL AND NOT w.legacy_unclaimed
	 AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL`, userID).Scan(&tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", fmt.Errorf("resolve merchant account: %w", err)
	}
	return tenant, nil
}
