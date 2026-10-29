package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CreateReset returns no token for an unknown or disabled account. The caller
// must give the same public response in either case and deliver known tokens
// only through the configured private recovery channel.
func (s *Store) CreateReset(ctx context.Context, email string) (string, string, error) {
	email, err := canonicalEmail(email)
	if err != nil {
		return "", "", nil
	}
	token, err := randomToken(32)
	if err != nil {
		return "", "", err
	}
	hash, err := tokenHash(token)
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var userID string
	err = s.pool.QueryRow(ctx, `INSERT INTO saas_password_resets(token_hash,user_id,expires_at)
	 SELECT $1,id,clock_timestamp()+interval '30 minutes' FROM saas_users WHERE email=$2 AND disabled_at IS NULL AND email_verified_at IS NOT NULL
	 RETURNING user_id`, hash, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("create password reset: %w", err)
	}
	return email, token, nil
}

func (s *Store) ResetPassword(ctx context.Context, token, password string) error {
	hash, err := tokenHash(token)
	if err != nil || !validPassword(password) {
		return ErrInvalid
	}
	passwordHash, err := s.passwordHash(ctx, password)
	if err != nil {
		return fmt.Errorf("hash reset password: %w", err)
	}
	return s.applyPasswordReset(ctx, hash, passwordHash)
}

func (s *Store) applyPasswordReset(ctx context.Context, tokenHash, passwordHash []byte) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start password reset: %w", err)
	}
	defer rollback(tx)
	userID, err := lockResetUser(ctx, tx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuth
	}
	if err != nil {
		return fmt.Errorf("read password reset: %w", err)
	}
	if err = finishPasswordReset(ctx, tx, userID, passwordHash); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockResetUser(ctx context.Context, tx pgx.Tx, hash []byte) (string, error) {
	var userID string
	if err := tx.QueryRow(ctx, `SELECT user_id FROM saas_password_resets WHERE token_hash=$1`, hash).Scan(&userID); err != nil {
		return "", err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM saas_users WHERE id=$1 AND disabled_at IS NULL FOR UPDATE`, userID).Scan(&userID); err != nil {
		return "", err
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_password_resets WHERE token_hash=$1 AND user_id=$2 AND used_at IS NULL AND expires_at>clock_timestamp())`, hash, userID).Scan(&valid); err != nil {
		return "", err
	}
	if !valid {
		return "", pgx.ErrNoRows
	}
	return userID, nil
}

func finishPasswordReset(ctx context.Context, tx pgx.Tx, userID string, hash []byte) error {
	queries := []string{
		`UPDATE saas_users SET password_hash=$2,auth_version=auth_version+1 WHERE id=$1`,
		`UPDATE saas_password_resets SET used_at=clock_timestamp() WHERE user_id=$1 AND used_at IS NULL`,
		`UPDATE saas_sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL`,
	}
	if _, err := tx.Exec(ctx, queries[0], userID, hash); err != nil {
		return fmt.Errorf("set reset password: %w", err)
	}
	for _, query := range queries[1:] {
		if _, err := tx.Exec(ctx, query, userID); err != nil {
			return fmt.Errorf("invalidate old credentials: %w", err)
		}
	}
	return event(ctx, tx, "", userID, "", "PASSWORD_RESET", "")
}
