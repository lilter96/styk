package postgres

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) SaveOrderActionSecret(ctx context.Context, secret orders.ActionSecret) (orders.ActionSecret, error) {
	if !validOrderActionSecret(secret) {
		return orders.ActionSecret{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.ActionSecret{}, ordersError("begin sealed action input", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = ordersLock(ctx, tx, secret.Scope); err != nil {
		return orders.ActionSecret{}, err
	}
	config, err := readOrderConfig(ctx, tx, secret.Scope)
	if err != nil || config.Revision != secret.ConfigRevision || !config.Enabled || !config.TestEnabled {
		return orders.ActionSecret{}, orders.ErrStale
	}
	args := append(ordersScopeArgs(secret.Scope), secret.PostingNumber, string(secret.Action), secret.Reference, secret.ConfigRevision, secret.Ciphertext, secret.CreatedAt, secret.ExpiresAt)
	_, err = tx.Exec(ctx, `INSERT INTO orders_action_secrets(tenant_id,moysklad_account,ozon_account,posting_number,action,reference,config_revision,ciphertext,created_at,expires_at) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10 WHERE $9<=clock_timestamp()+interval '1 minute' AND $10>clock_timestamp() AND $10<=clock_timestamp()+interval '1 day' ON CONFLICT DO NOTHING`, args...)
	if err != nil {
		return orders.ActionSecret{}, ordersError("seal exact action input", err)
	}
	stored, err := readOrderActionSecret(ctx, tx, secret.Scope, secret.PostingNumber, secret.Action, orders.ProtectedActionInput{Reference: secret.Reference, Revision: secret.ConfigRevision})
	if err != nil {
		return orders.ActionSecret{}, err
	}
	return stored, ordersRowsError("commit sealed action input", tx.Commit(ctx))
}

func validOrderActionSecret(secret orders.ActionSecret) bool {
	digest, err := hex.DecodeString(secret.Reference)
	return err == nil && len(digest) == 32 && secret.Scope.Validate() == nil && secret.Action.Valid() && !secret.Action.IsMirror() && secret.PostingNumber != "" && secret.ConfigRevision > 0 && len(secret.Ciphertext) >= 29 && len(secret.Ciphertext) <= 132096 && !secret.CreatedAt.IsZero() && secret.ExpiresAt.After(secret.CreatedAt) && secret.ExpiresAt.Sub(secret.CreatedAt) <= 24*time.Hour
}

func (s *Store) OrderActionSecret(ctx context.Context, scope orders.Scope, number string, action orders.Action, ref orders.ProtectedActionInput) (orders.ActionSecret, error) {
	if scope.Validate() != nil || number == "" || !action.Valid() || ref.Revision < 1 {
		return orders.ActionSecret{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readOrderActionSecret(ctx, s.Pool, scope, number, action, ref)
}

func readOrderActionSecret(ctx context.Context, q ordersQuerier, scope orders.Scope, number string, action orders.Action, ref orders.ProtectedActionInput) (orders.ActionSecret, error) {
	secret := orders.ActionSecret{Scope: scope, PostingNumber: number, Action: action, Reference: ref.Reference, ConfigRevision: ref.Revision}
	args := append(ordersScopeArgs(scope), number, string(action), ref.Reference, ref.Revision)
	err := q.QueryRow(ctx, `SELECT s.ciphertext,s.created_at,s.expires_at FROM orders_action_secrets s JOIN orders_configs c USING(tenant_id,moysklad_account,ozon_account) WHERE s.tenant_id=$1 AND s.moysklad_account=$2 AND s.ozon_account=$3 AND s.posting_number=$4 AND s.action=$5 AND s.reference=$6 AND s.config_revision=$7 AND c.revision=s.config_revision AND s.expires_at>clock_timestamp()`, args...).Scan(&secret.Ciphertext, &secret.CreatedAt, &secret.ExpiresAt)
	if err != nil {
		return orders.ActionSecret{}, ordersError("read sealed action input", err)
	}
	return secret, nil
}
