package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CreateVerification returns a deliverable token only for an unverified user.
// The public endpoint must respond identically when the address is unknown.
func (s *Store) CreateVerification(ctx context.Context, email string) (string, string, error) {
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
	err = s.pool.QueryRow(ctx, `INSERT INTO saas_email_verifications(token_hash,user_id,expires_at)
	 SELECT $1,id,clock_timestamp()+interval '24 hours' FROM saas_users WHERE email=$2 AND disabled_at IS NULL AND email_verified_at IS NULL
	 RETURNING user_id`, hash, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("create email verification: %w", err)
	}
	return email, token, nil
}

func (s *Store) VerifyEmail(ctx context.Context, token string) error {
	hash, err := tokenHash(token)
	if err != nil {
		return ErrAuth
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start email verification: %w", err)
	}
	defer rollback(tx)
	userID, err := lockVerificationUser(ctx, tx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAuth
	}
	if err != nil {
		return fmt.Errorf("read email verification: %w", err)
	}
	if err = finishVerification(ctx, tx, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockVerificationUser(ctx context.Context, tx pgx.Tx, hash []byte) (string, error) {
	var userID string
	if err := tx.QueryRow(ctx, `SELECT user_id FROM saas_email_verifications WHERE token_hash=$1`, hash).Scan(&userID); err != nil {
		return "", err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM saas_users WHERE id=$1 AND disabled_at IS NULL AND email_verified_at IS NULL FOR UPDATE`, userID).Scan(&userID); err != nil {
		return "", err
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_email_verifications WHERE token_hash=$1 AND user_id=$2 AND used_at IS NULL AND expires_at>clock_timestamp())`, hash, userID).Scan(&valid); err != nil {
		return "", err
	}
	if !valid {
		return "", pgx.ErrNoRows
	}
	return userID, nil
}

func finishVerification(ctx context.Context, tx pgx.Tx, userID string) error {
	if _, err := tx.Exec(ctx, `UPDATE saas_users SET email_verified_at=clock_timestamp() WHERE id=$1`, userID); err != nil {
		return fmt.Errorf("verify email: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_email_verifications SET used_at=clock_timestamp() WHERE user_id=$1 AND used_at IS NULL`, userID); err != nil {
		return fmt.Errorf("consume verification tokens: %w", err)
	}
	return event(ctx, tx, "", userID, "", "EMAIL_VERIFIED", "")
}
