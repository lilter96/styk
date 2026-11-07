package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) CarriageReviewContext(ctx context.Context, scope orders.Scope, actor string) (orders.ReviewContext, error) {
	if scope.Validate() != nil {
		return orders.ReviewContext{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return orders.ReviewContext{}, ordersError("begin carriage source review", err)
	}
	defer rollbackMerchantMutation(tx)
	if _, err = ordersActor(ctx, tx, scope.Tenant, actor); err != nil {
		return orders.ReviewContext{}, err
	}
	config, credentials, err := carriageCurrent(ctx, tx, scope)
	if err != nil {
		return orders.ReviewContext{}, err
	}
	return orders.ReviewContext{Config: config, Credentials: credentials}, ordersRowsError("commit carriage source review", tx.Commit(ctx))
}

func carriageCurrent(ctx context.Context, tx pgx.Tx, scope orders.Scope) (orders.Config, orders.CredentialRevisions, error) {
	config, err := readOrderConfig(ctx, tx, scope)
	if err != nil {
		return config, orders.CredentialRevisions{}, err
	}
	if !config.ReadsEnabled() || config.Revision < 1 {
		return config, orders.CredentialRevisions{}, orders.ErrBlocked
	}
	if err = validateSourceReadOwner(ctx, tx, scope); err != nil {
		return config, orders.CredentialRevisions{}, err
	}
	credentials, err := ordersCredentials(ctx, tx, scope)
	if err != nil {
		return config, credentials, err
	}
	if credentials.Ozon < 1 || credentials.Moysklad < 1 {
		return config, credentials, orders.ErrBlocked
	}
	return config, credentials, nil
}

func readCarriagePreview(ctx context.Context, q ordersQuerier, scope orders.Scope, id string) (orders.CarriagePreview, error) {
	var result orders.CarriagePreview
	var raw []byte
	var actor string
	err := q.QueryRow(ctx, `SELECT preview,actor FROM orders_carriage_previews WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4`, append(ordersScopeArgs(scope), id)...).Scan(&raw, &actor)
	if err != nil {
		return result, ordersError("read carriage review", err)
	}
	if err = ordersDecode(raw, &result); err != nil {
		return result, err
	}
	result.Actor = actor
	return result, nil
}

func readCarriageWork(ctx context.Context, q ordersQuerier, scope orders.Scope, id string) (orders.CarriageWork, error) {
	var result orders.CarriageWork
	var raw []byte
	err := q.QueryRow(ctx, `SELECT work FROM orders_carriage_work WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4`, append(ordersScopeArgs(scope), id)...).Scan(&raw)
	if err != nil {
		return result, ordersError("read carriage operation", err)
	}
	if err = ordersDecode(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) CarriageHistory(ctx context.Context, scope orders.Scope, limit int) ([]orders.CarriageWork, error) {
	if scope.Validate() != nil || limit < 1 || limit > 100 {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	rows, err := s.Pool.Query(ctx, `SELECT work FROM orders_carriage_work WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 ORDER BY created_at DESC,id DESC LIMIT $4`, append(ordersScopeArgs(scope), limit)...)
	if err != nil {
		return nil, ordersError("read carriage history", err)
	}
	defer rows.Close()
	result := make([]orders.CarriageWork, 0, limit)
	for rows.Next() {
		var raw []byte
		var work orders.CarriageWork
		if err = rows.Scan(&raw); err != nil {
			return nil, ordersError("scan carriage history", err)
		}
		if err = ordersDecode(raw, &work); err != nil {
			return nil, err
		}
		result = append(result, work)
	}
	return result, ordersRowsError("read carriage history", rows.Err())
}
