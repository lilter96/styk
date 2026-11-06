package identity

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const supportGrantMatch = `e.id=$1 AND e.action='PLATFORM_SUPPORT_START'
 AND e.actor_user_id=$2 AND e.session_audit_id=$3
 AND split_part(e.target, ':', 2)=$4`

const supportNotEnded = `NOT EXISTS (
 SELECT 1 FROM saas_security_events x WHERE x.workspace_id=e.workspace_id AND x.id>e.id
 AND x.action='PLATFORM_SUPPORT_END' AND x.actor_user_id=e.actor_user_id
 AND x.session_audit_id=e.session_audit_id AND x.target=e.id::text
)`

type SupportAccess struct {
	Workspace Workspace
	User      User
	Expires   time.Time
}

// StartSupport issues an opaque grant. The append-only audit row stores its
// digest, the original login, and the exact verified account it was issued for.
func (s *Store) StartSupport(ctx context.Context, admin Session, tenant string) (string, error) {
	if tenant == "" || len(tenant) > 256 || admin.AuditID == "" || admin.User.ID == "" {
		return "", ErrInvalid
	}
	token, err := randomToken(32)
	if err != nil {
		return "", fmt.Errorf("generate support grant: %w", err)
	}
	hash, err := tokenHash(token)
	if err != nil {
		return "", fmt.Errorf("hash support grant: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var id int64
	err = s.pool.QueryRow(ctx, `INSERT INTO saas_security_events(workspace_id,actor_user_id,session_audit_id,action,target)
 SELECT a.tenant_id,$2,$3,'PLATFORM_SUPPORT_START',a.user_id||':'||$4
 FROM saas_merchant_accounts a
 JOIN saas_memberships m ON (m.workspace_id,m.user_id)=(a.tenant_id,a.user_id)
 JOIN saas_workspaces w ON w.id=a.tenant_id
 JOIN saas_users u ON u.id=a.user_id
 WHERE a.tenant_id=$1 AND a.user_id<>$2 AND m.role='OWNER' AND m.revoked_at IS NULL
 AND NOT w.legacy_unclaimed AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL
 RETURNING id`, tenant, admin.User.ID, admin.AuditID, hex.EncodeToString(hash)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", fmt.Errorf("record support grant: %w", err)
	}
	return strconv.FormatInt(id, 10) + "." + token, nil
}

func supportGrantArgs(admin Session, proof string) ([]any, error) {
	rawID, token, ok := strings.Cut(proof, ".")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if !ok || err != nil || id <= 0 || len(token) != 43 || admin.AuditID == "" {
		return nil, ErrAuth
	}
	hash, err := tokenHash(token)
	if err != nil {
		return nil, ErrAuth
	}
	return []any{id, admin.User.ID, admin.AuditID, hex.EncodeToString(hash)}, nil
}

// Support verifies the grant, its original login, and the current merchant
// binding. No target session or target credential is created.
func (s *Store) Support(ctx context.Context, admin Session, proof string) (SupportAccess, error) {
	args, err := supportGrantArgs(admin, proof)
	if err != nil {
		return SupportAccess{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var access SupportAccess
	err = s.pool.QueryRow(ctx, `SELECT a.tenant_id,w.name,u.id,u.email,LEAST(e.created_at+interval '15 minutes', $5::timestamptz)
 FROM saas_security_events e
 JOIN saas_merchant_accounts a ON a.tenant_id=e.workspace_id AND a.user_id=split_part(e.target, ':', 1)
 JOIN saas_memberships m ON (m.workspace_id,m.user_id)=(a.tenant_id,a.user_id)
 JOIN saas_workspaces w ON w.id=a.tenant_id
 JOIN saas_users u ON u.id=a.user_id
 WHERE `+supportGrantMatch+` AND `+supportNotEnded+`
 AND e.created_at+interval '15 minutes'>clock_timestamp()
 AND m.role='OWNER' AND m.revoked_at IS NULL AND NOT w.legacy_unclaimed
 AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL`, append(args, admin.Expires)...).Scan(
		&access.Workspace.ID, &access.Workspace.Name, &access.User.ID, &access.User.Email, &access.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return SupportAccess{}, ErrAuth
	}
	if err != nil {
		return SupportAccess{}, fmt.Errorf("read support grant: %w", err)
	}
	return access, nil
}

// EndSupport appends a revocation for the original grant. Locking the grant
// serializes retries without mutating the audit record.
func (s *Store) EndSupport(ctx context.Context, admin Session, proof string) error {
	args, err := supportGrantArgs(admin, proof)
	if err != nil {
		return nil // A stale or malformed cookie grants nothing and can be cleared.
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin support revocation: %w", err)
	}
	defer rollback(tx)
	var workspace string
	err = tx.QueryRow(ctx, `SELECT e.workspace_id FROM saas_security_events e WHERE `+supportGrantMatch+` FOR UPDATE`, args...).Scan(&workspace)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock support grant: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO saas_security_events(workspace_id,actor_user_id,session_audit_id,action,target)
 SELECT e.workspace_id,e.actor_user_id,e.session_audit_id,'PLATFORM_SUPPORT_END',e.id::text
 FROM saas_security_events e WHERE e.id=$1 AND `+supportNotEnded, args[0])
	if err != nil {
		return fmt.Errorf("record support revocation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit support revocation: %w", err)
	}
	return nil
}
