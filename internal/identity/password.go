package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ChangePassword verifies the current secret, rotates the password and keeps
// only the calling session. The auth version bump invalidates every other
// session created with the previous password.
func (s *Store) ChangePassword(ctx context.Context, session Session, current, next string) error {
	if session.User.ID == "" || session.AuditID == "" || !validPassword(current) || !validPassword(next) {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	hash, version, err := s.currentPassword(ctx, session.User.ID)
	if err != nil {
		return err
	}
	if !s.passwordMatches(ctx, hash, current) {
		return ErrAuth
	}
	nextHash, err := s.passwordHash(ctx, next)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	return s.replacePassword(ctx, session, version, nextHash)
}

func (s *Store) currentPassword(ctx context.Context, userID string) ([]byte, int64, error) {
	var hash []byte
	var version int64
	err := s.pool.QueryRow(ctx, `SELECT password_hash,auth_version FROM saas_users
	 WHERE id=$1 AND disabled_at IS NULL AND email_verified_at IS NOT NULL`, userID).Scan(&hash, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrAuth
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read current password: %w", err)
	}
	return hash, version, nil
}

func (s *Store) replacePassword(ctx context.Context, session Session, version int64, hash []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start password change: %w", err)
	}
	defer rollback(tx)
	// Lock the user first so concurrent password changes serialize before any
	// session row is locked, and a stale version cannot overwrite a newer one.
	var current int64
	err = tx.QueryRow(ctx, `SELECT auth_version FROM saas_users
	 WHERE id=$1 AND disabled_at IS NULL AND email_verified_at IS NOT NULL FOR UPDATE`, session.User.ID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuth
	}
	if err != nil {
		return fmt.Errorf("lock password owner: %w", err)
	}
	if current != version {
		return ErrAuth
	}
	var nextVersion int64
	err = tx.QueryRow(ctx, `UPDATE saas_users SET password_hash=$2,auth_version=auth_version+1
	 WHERE id=$1 RETURNING auth_version`, session.User.ID, hash).Scan(&nextVersion)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if err = keepOnlySession(ctx, tx, session, nextVersion); err != nil {
		return err
	}
	if err = event(ctx, tx, "", session.User.ID, session.AuditID, "PASSWORD_CHANGED", ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func keepOnlySession(ctx context.Context, tx pgx.Tx, session Session, version int64) error {
	var userID string
	err := tx.QueryRow(ctx, `SELECT user_id FROM saas_sessions
	 WHERE audit_id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp()
	 FOR UPDATE`, session.AuditID, session.User.ID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuth
	}
	if err != nil {
		return fmt.Errorf("lock password session: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_sessions SET revoked_at=clock_timestamp()
	 WHERE user_id=$1 AND audit_id<>$2 AND revoked_at IS NULL`, session.User.ID, session.AuditID); err != nil {
		return fmt.Errorf("revoke other sessions: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE saas_sessions SET user_auth_version=$3
	 WHERE user_id=$1 AND audit_id=$2`, session.User.ID, session.AuditID, version); err != nil {
		return fmt.Errorf("keep current session: %w", err)
	}
	return nil
}
