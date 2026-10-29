package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type Member struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

func ValidRole(role string) bool {
	return role == "OWNER" || role == "ADMIN" || role == "OPERATOR" || role == "VIEWER"
}

func CanRead(role string) bool    { return ValidRole(role) }
func CanOperate(role string) bool { return role == "OWNER" || role == "ADMIN" || role == "OPERATOR" }
func CanAdmin(role string) bool   { return role == "OWNER" || role == "ADMIN" }

func (s *Store) Workspaces(ctx context.Context, userID string) ([]Workspace, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT w.id,w.name,m.role FROM saas_workspaces w
	 JOIN saas_memberships m ON m.workspace_id=w.id JOIN saas_users u ON u.id=m.user_id
	 WHERE m.user_id=$1 AND m.revoked_at IS NULL AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL AND NOT w.legacy_unclaimed
	 ORDER BY w.name,w.id LIMIT 101`, userID)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()
	items := make([]Workspace, 0)
	for rows.Next() {
		var workspace Workspace
		if err = rows.Scan(&workspace.ID, &workspace.Name, &workspace.Role); err != nil {
			return nil, fmt.Errorf("scan workspace: %w", err)
		}
		items = append(items, workspace)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	return items, nil
}

// Role is resolved on every request; a role change or revocation affects an
// already authenticated session without requiring the browser to log in again.
func (s *Store) Role(ctx context.Context, userID, workspaceID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var role string
	err := s.pool.QueryRow(ctx, `SELECT m.role FROM saas_memberships m JOIN saas_workspaces w ON w.id=m.workspace_id
	 JOIN saas_users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND m.revoked_at IS NULL
	 AND NOT w.legacy_unclaimed AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL`, workspaceID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", fmt.Errorf("read workspace membership: %w", err)
	}
	return role, nil
}

func (s *Store) CreateWorkspace(ctx context.Context, session Session, name string) (Workspace, error) {
	if !validName(name) {
		return Workspace{}, ErrInvalid
	}
	id, err := identifier("w_")
	if err != nil {
		return Workspace{}, err
	}
	workspace := Workspace{ID: id, Name: name, Role: "OWNER"}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Workspace{}, fmt.Errorf("start workspace creation: %w", err)
	}
	defer rollback(tx)
	if err = lockSession(ctx, tx, session); err != nil {
		return Workspace{}, err
	}
	if err = insertWorkspace(ctx, tx, workspace, session.User.ID, session.AuditID); err != nil {
		return Workspace{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Workspace{}, err
	}
	return workspace, nil
}

func lockSession(ctx context.Context, tx pgx.Tx, session Session) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT s.audit_id FROM saas_sessions s JOIN saas_users u ON u.id=s.user_id
	 WHERE s.audit_id=$1 AND u.id=$2 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
	 AND s.user_auth_version=u.auth_version AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL
	 FOR SHARE OF s,u`, session.AuditID, session.User.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuth
	}
	if err != nil {
		return fmt.Errorf("lock active session: %w", err)
	}
	return nil
}

func (s *Store) Members(ctx context.Context, session Session, workspace string) ([]Member, error) {
	role, err := s.Role(ctx, session.User.ID, workspace)
	if errors.Is(err, ErrForbidden) || (err == nil && !CanAdmin(role)) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT u.id,u.email,m.role FROM saas_memberships m JOIN saas_users u ON u.id=m.user_id
	 WHERE m.workspace_id=$1 AND m.revoked_at IS NULL ORDER BY u.email LIMIT 101`, workspace)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	items := make([]Member, 0)
	for rows.Next() {
		var member Member
		if err = rows.Scan(&member.UserID, &member.Email, &member.Role); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		items = append(items, member)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	return items, nil
}
