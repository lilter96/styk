package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) CommissionReviewContext(ctx context.Context, scope orders.Scope) (orders.ReviewContext, error) {
	if scope.Validate() != nil {
		return orders.ReviewContext{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return orders.ReviewContext{}, ordersError("begin commission reference review", err)
	}
	defer rollbackMerchantMutation(tx)
	result := orders.ReviewContext{}
	if result.Config, err = readOrderConfig(ctx, tx, scope); err != nil {
		return result, err
	}
	if result.Credentials, err = ordersCredentials(ctx, tx, scope); err != nil {
		return result, err
	}
	if result.ProductMappings, err = readOrderProductMappings(ctx, tx, scope); err != nil {
		return result, err
	}
	return result, ordersRowsError("commit commission reference review", tx.Commit(ctx))
}

func (s *Store) CommissionPreview(ctx context.Context, scope orders.Scope, id string) (orders.CommissionPreview, error) {
	if scope.Validate() != nil || id == "" {
		return orders.CommissionPreview{}, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return readCommissionPreview(ctx, s.Pool, scope, id)
}

func readCommissionPreview(ctx context.Context, q ordersQuerier, scope orders.Scope, id string) (orders.CommissionPreview, error) {
	var result orders.CommissionPreview
	var raw []byte
	err := q.QueryRow(ctx, `SELECT preview,actor,automatic FROM orders_finance_previews WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND id=$4`, append(ordersScopeArgs(scope), id)...).Scan(&raw, &result.Actor, &result.Automatic)
	if err != nil {
		return result, ordersError("read commission preview", err)
	}
	actor, automatic := result.Actor, result.Automatic
	if err = ordersDecode(raw, &result); err != nil {
		return result, err
	}
	result.Actor, result.Automatic = actor, automatic
	return result, nil
}

func readCommissionWork(ctx context.Context, q ordersQuerier, scope orders.Scope, id string) (orders.CommissionWork, error) {
	var work orders.CommissionWork
	var raw []byte
	err := q.QueryRow(ctx, `SELECT w.work,COALESCE(p.source_changed,false) FROM orders_finance_work w LEFT JOIN orders_finance_periods p ON p.work_id=w.id WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.id=$4`, append(ordersScopeArgs(scope), id)...).Scan(&raw, &work.SourceChanged)
	if err != nil {
		return work, ordersError("read commission work", err)
	}
	changed := work.SourceChanged
	if err = ordersDecode(raw, &work); err != nil {
		return work, err
	}
	work.SourceChanged = work.SourceChanged || changed
	return work, nil
}

func readCommissionPeriod(ctx context.Context, q ordersQuerier, scope orders.Scope, period orders.RealizationPeriod) (*orders.CommissionWork, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT work_id FROM orders_finance_periods WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND report_year=$4 AND report_month=$5`, append(ordersScopeArgs(scope), period.Year, period.Month)...).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ordersError("read canonical commission period", err)
	}
	work, err := readCommissionWork(ctx, q, scope, id)
	return &work, err
}

func (s *Store) CommissionHistory(ctx context.Context, scope orders.Scope, limit int) ([]orders.CommissionWork, error) {
	if scope.Validate() != nil || limit < 1 || limit > 100 {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	rows, err := s.Pool.Query(ctx, `SELECT w.work,COALESCE(p.source_changed,false) FROM orders_finance_work w LEFT JOIN orders_finance_periods p ON p.work_id=w.id WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 ORDER BY w.created_at DESC,w.id DESC LIMIT $4`, append(ordersScopeArgs(scope), limit)...)
	if err != nil {
		return nil, ordersError("read commission operation history", err)
	}
	defer rows.Close()
	result := make([]orders.CommissionWork, 0, limit)
	for rows.Next() {
		var raw []byte
		var changed bool
		var work orders.CommissionWork
		if err = rows.Scan(&raw, &changed); err != nil {
			return nil, ordersError("scan commission operation", err)
		}
		if err = ordersDecode(raw, &work); err != nil {
			return nil, err
		}
		work.SourceChanged = work.SourceChanged || changed
		result = append(result, work)
	}
	return result, ordersRowsError("read commission operation history", rows.Err())
}
