package postgres

import (
	"bytes"
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) SaveOrderSecret(ctx context.Context, secret orders.PostingSecret) error {
	if err := secret.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ordersError("begin encrypted order secret", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = ordersLock(ctx, tx, secret.Scope); err != nil {
		return err
	}
	config, err := readOrderConfig(ctx, tx, secret.Scope)
	if err != nil {
		return err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	if err = insertOrderSecret(ctx, tx, secret, config, now); err != nil {
		return err
	}
	return ordersRowsError("commit encrypted order secret", tx.Commit(ctx))
}

func insertOrderSecret(ctx context.Context, tx pgx.Tx, secret orders.PostingSecret, config orders.Config, now time.Time) error {
	if err := secret.Validate(); err != nil {
		return err
	}
	if !config.PIIEnabled || secret.Scope != config.Scope || secret.CreatedAt.After(now.Add(time.Minute)) || !secret.ExpiresAt.After(now) || secret.ExpiresAt.After(now.Add(time.Duration(config.PIIRetentionSeconds)*time.Second)) {
		return orders.ErrBlocked
	}
	args := append(ordersScopeArgs(secret.Scope), secret.PostingNumber, secret.Revision, secret.Purpose, secret.Ciphertext, secret.CreatedAt, secret.ExpiresAt)
	if _, err := tx.Exec(ctx, `INSERT INTO orders_posting_secrets(tenant_id,moysklad_account,ozon_account,posting_number,revision,purpose,ciphertext,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`, args...); err != nil {
		return ordersError("save encrypted order secret", err)
	}
	stored, err := readOrderSecret(ctx, tx, secret.Scope, secret.PostingNumber, secret.Revision, false)
	if err != nil {
		return err
	}
	if stored.Purpose != secret.Purpose || !bytes.Equal(stored.Ciphertext, secret.Ciphertext) || !stored.CreatedAt.Equal(secret.CreatedAt) || !stored.ExpiresAt.Equal(secret.ExpiresAt) {
		return orders.ErrConflict
	}
	return nil
}

func (s *Store) OrderSecret(ctx context.Context, scope orders.Scope, number string, revision int64) (orders.PostingSecret, error) {
	if scope.Validate() != nil || number == "" || revision < 1 {
		return orders.PostingSecret{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readOrderSecret(ctx, s.Pool, scope, number, revision, true)
}

func readOrderSecret(ctx context.Context, q ordersQuerier, scope orders.Scope, number string, revision int64, live bool) (orders.PostingSecret, error) {
	secret := orders.PostingSecret{Scope: scope, PostingNumber: number, Revision: revision}
	args := append(ordersScopeArgs(scope), number, revision, live)
	err := q.QueryRow(ctx, `SELECT s.purpose,s.ciphertext,s.created_at,s.expires_at FROM orders_posting_secrets s JOIN orders_configs c USING(tenant_id,moysklad_account,ozon_account) JOIN orders_config_revisions r ON r.tenant_id=c.tenant_id AND r.moysklad_account=c.moysklad_account AND r.ozon_account=c.ozon_account AND r.revision=c.revision WHERE s.tenant_id=$1 AND s.moysklad_account=$2 AND s.ozon_account=$3 AND s.posting_number=$4 AND s.revision=$5 AND (NOT $6 OR (s.expires_at>clock_timestamp() AND (r.config->>'pii_enabled')::boolean))`, args...).Scan(&secret.Purpose, &secret.Ciphertext, &secret.CreatedAt, &secret.ExpiresAt)
	if err != nil {
		return orders.PostingSecret{}, ordersError("read encrypted order secret", err)
	}
	return secret, nil
}

func (s *Store) DeleteExpiredOrderSecrets(ctx context.Context, _ time.Time, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tag, err := s.Pool.Exec(ctx, `WITH expired AS (SELECT tenant_id,moysklad_account,ozon_account,posting_number,revision FROM orders_posting_secrets WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED) DELETE FROM orders_posting_secrets s USING expired e WHERE (s.tenant_id,s.moysklad_account,s.ozon_account,s.posting_number,s.revision)=(e.tenant_id,e.moysklad_account,e.ozon_account,e.posting_number,e.revision)`, limit)
	if err != nil {
		return 0, ordersError("expire encrypted order secrets", err)
	}
	return int(tag.RowsAffected()), nil
}
