package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CreateSession stores only a digest of the bearer token. The independent CSRF
// value is delivered only to an authenticated same-origin browser.
func (s *Store) CreateSession(ctx context.Context, user User) (Session, string, error) {
	token, err := randomToken(32)
	if err != nil {
		return Session{}, "", err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return Session{}, "", err
	}
	audit, err := randomToken(16)
	if err != nil {
		return Session{}, "", err
	}
	hash, err := tokenHash(token)
	if err != nil {
		return Session{}, "", err
	}
	session := Session{AuditID: "user." + audit, User: user, CSRF: csrf}
	if err = s.insertSession(ctx, &session, hash); err != nil {
		return Session{}, "", err
	}
	return session, token, nil
}

func (s *Store) insertSession(ctx context.Context, session *Session, hash []byte) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer rollback(tx)
	err = tx.QueryRow(ctx, `INSERT INTO saas_sessions(audit_id,token_hash,csrf_token,user_id,user_auth_version,expires_at)
	 SELECT $1,$2,$3,id,$5,clock_timestamp()+interval '8 hours' FROM saas_users WHERE id=$4 AND auth_version=$5 AND disabled_at IS NULL AND email_verified_at IS NOT NULL
	 RETURNING expires_at`, session.AuditID, hash, session.CSRF, session.User.ID, session.User.AuthVersion).Scan(&session.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuth
	}
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	if err = event(ctx, tx, "", session.User.ID, session.AuditID, "LOGIN", ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Session(ctx context.Context, token string) (Session, error) {
	hash, err := tokenHash(token)
	if err != nil {
		return Session{}, ErrAuth
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var session Session
	err = s.pool.QueryRow(ctx, `SELECT s.audit_id,s.csrf_token,s.expires_at,u.id,u.email,u.auth_version FROM saas_sessions s
	 JOIN saas_users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL
	 AND s.expires_at>clock_timestamp() AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL AND s.user_auth_version=u.auth_version`, hash).Scan(&session.AuditID, &session.CSRF, &session.Expires, &session.User.ID, &session.User.Email, &session.User.AuthVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrAuth
	}
	if err != nil {
		return Session{}, fmt.Errorf("read session: %w", err)
	}
	return session, nil
}

func (s *Store) RevokeSession(ctx context.Context, session Session) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	defer rollback(tx)
	result, err := tx.Exec(ctx, `UPDATE saas_sessions SET revoked_at=clock_timestamp() WHERE audit_id=$1 AND user_id=$2 AND revoked_at IS NULL`, session.AuditID, session.User.ID)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrAuth
	}
	if err = event(ctx, tx, "", session.User.ID, session.AuditID, "LOGOUT", ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
