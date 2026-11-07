package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) GuardCommissionDispatch(ctx context.Context, supplied orders.CommissionWork, begin bool) (bool, error) {
	if supplied.Scope.Validate() != nil || supplied.ID == "" || supplied.ClaimToken == "" {
		return false, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, ordersError("begin commission dispatch guard", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockOrderReviewScope(ctx, tx, supplied.Scope); err != nil {
		return false, err
	}
	work, err := lockCommissionWork(ctx, tx, supplied.Scope, supplied.ID, supplied.ClaimToken)
	if err != nil {
		return false, err
	}
	err = commissionDispatchCurrent(ctx, tx, work, supplied, begin)
	if errors.Is(err, orders.ErrBlocked) || errors.Is(err, orders.ErrStale) || errors.Is(err, orders.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if begin {
		now, clockErr := ordersClock(ctx, tx)
		if clockErr != nil {
			return false, clockErr
		}
		work.State, work.DispatchCount = orders.StateDispatching, 1
		if err = writeCommissionWork(ctx, tx, &work, now); err != nil {
			return false, err
		}
		if err = insertCommissionAttempt(ctx, tx, work, nil); err != nil {
			return false, err
		}
	}
	return true, ordersRowsError("commit commission dispatch guard", tx.Commit(ctx))
}

func commissionDispatchCurrent(ctx context.Context, tx pgx.Tx, work, supplied orders.CommissionWork, begin bool) error {
	if work.Plan.Fingerprint != supplied.Plan.Fingerprint || work.ConfigRevision != supplied.ConfigRevision || work.Credentials != supplied.Credentials {
		return orders.ErrStale
	}
	if !begin && work.State.ReadOnly() && work.DispatchCount == 1 {
		return commissionReadbackCurrent(ctx, tx, work)
	}
	if work.State != orders.StateClaimed || work.DispatchCount != 0 {
		return orders.ErrBlocked
	}
	config, err := readOrderConfig(ctx, tx, work.Scope)
	if err != nil {
		return err
	}
	var actor, owner string
	err = tx.QueryRow(ctx, `SELECT actor,owner_user_id FROM orders_finance_work WHERE id=$1`, work.ID).Scan(&actor, &owner)
	if err != nil {
		return ordersError("read commission write authority", err)
	}
	if err = ordersOwnerCurrent(ctx, tx, work.Scope.Tenant, owner); err != nil {
		return err
	}
	if work.Automatic && (config.Finance == nil || !config.Finance.Automatic || actor != commissionActor(*config.Finance)) {
		return orders.ErrBlocked
	}
	preview := orders.CommissionPreview{Plan: work.Plan}
	if err = validateCommissionPreview(ctx, tx, preview, config, owner); err != nil {
		return err
	}
	if err = validateOrderReadAuthority(ctx, tx, config); err != nil {
		return err
	}
	return commissionPeriodCurrent(ctx, tx, work)
}

func commissionPeriodCurrent(ctx context.Context, tx pgx.Tx, work orders.CommissionWork) error {
	var id, fingerprint string
	var changed bool
	args := append(ordersScopeArgs(work.Scope), work.Plan.Source.Period.Year, work.Plan.Source.Period.Month)
	err := tx.QueryRow(ctx, `SELECT work_id,current_fingerprint,source_changed FROM orders_finance_periods WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND report_year=$4 AND report_month=$5 FOR UPDATE`, args...).Scan(&id, &fingerprint, &changed)
	if err != nil {
		return ordersError("lock current commission period", err)
	}
	if id != work.ID || fingerprint != work.Plan.Source.Fingerprint || changed {
		return orders.ErrStale
	}
	return nil
}

func commissionReadbackCurrent(ctx context.Context, tx pgx.Tx, work orders.CommissionWork) error {
	var owner string
	err := tx.QueryRow(ctx, `SELECT owner_user_id FROM orders_finance_work WHERE id=$1`, work.ID).Scan(&owner)
	if err != nil {
		return ordersError("read original commission owner", err)
	}
	if err = ordersOwnerCurrent(ctx, tx, work.Scope.Tenant, owner); err != nil {
		return err
	}
	revisions, err := ordersCredentials(ctx, tx, work.Scope)
	if err != nil {
		return err
	}
	if revisions != work.Credentials {
		return orders.ErrStale
	}
	config, err := readOrderConfig(ctx, tx, work.Scope)
	if err != nil {
		return err
	}
	// A paused writer can still reconcile its one previously sent request.
	// Explicit source-read revocation or disconnected accounts stops reads.
	if !config.ReadsEnabled() {
		return orders.ErrBlocked
	}
	if config.ReadEnabled {
		if err = validateSourceReadOwner(ctx, tx, work.Scope); err != nil {
			return err
		}
	}
	var connected bool
	err = tx.QueryRow(ctx, `SELECT count(*)=2 FROM stock_connections WHERE tenant_id=$1 AND ((source='moysklad' AND account_id=$2) OR (source='ozon' AND account_id=$3))`, ordersScopeArgs(work.Scope)...).Scan(&connected)
	if err != nil {
		return ordersError("check commission reconciliation accounts", err)
	}
	if !connected {
		return orders.ErrBlocked
	}
	return nil
}
