package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) SaveCarriagePreview(ctx context.Context, preview orders.CarriagePreview) (orders.CarriagePreview, error) {
	if preview.ID != preview.Plan.ID || preview.Plan.Scope.Validate() != nil || preview.Actor == "" {
		return preview, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return preview, ordersError("begin carriage review", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockOrderReviewScope(ctx, tx, preview.Plan.Scope); err != nil {
		return preview, err
	}
	owner, err := ordersActor(ctx, tx, preview.Plan.Scope.Tenant, preview.Actor)
	if err != nil {
		return preview, err
	}
	if err = validateCarriagePreview(ctx, tx, preview); err != nil {
		return preview, err
	}
	raw, err := ordersJSON(preview)
	if err != nil {
		return preview, err
	}
	args := append(ordersScopeArgs(preview.Plan.Scope), preview.ID, preview.Plan.ConfigRevision, raw, preview.Actor, owner, preview.Plan.CreatedAt, preview.Plan.ExpiresAt)
	_, err = tx.Exec(ctx, `INSERT INTO orders_carriage_previews(tenant_id,moysklad_account,ozon_account,id,config_revision,preview,actor,owner_user_id,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, args...)
	if err != nil {
		return preview, ordersError("seal carriage review", err)
	}
	return preview, ordersRowsError("commit carriage review", tx.Commit(ctx))
}

func validateCarriagePreview(ctx context.Context, tx pgx.Tx, preview orders.CarriagePreview) error {
	config, credentials, err := carriageCurrent(ctx, tx, preview.Plan.Scope)
	if err != nil {
		return err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	plan := preview.Plan
	if config.Revision != plan.ConfigRevision || credentials != plan.Credentials || !plan.ExpiresAt.After(now) || plan.CreatedAt.After(now.Add(60e9)) {
		return orders.ErrStale
	}
	if len(preview.Validation) != 0 {
		return nil
	}
	if plan.Validate() != nil || len(plan.ReadinessIssues(now)) != 0 {
		return orders.ErrBlocked
	}
	return nil
}
