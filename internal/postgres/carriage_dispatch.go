package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) GuardCarriageDispatch(ctx context.Context, supplied orders.CarriageWork, begin bool) (bool, error) {
	if supplied.Scope.Validate() != nil || supplied.ID == "" || supplied.ClaimToken == "" {
		return false, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, ordersError("begin carriage dispatch guard", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockCarriageTarget(ctx, tx, supplied.Plan); err != nil {
		return false, err
	}
	if err = lockOrderReviewScope(ctx, tx, supplied.Scope); err != nil {
		return false, err
	}
	work, err := lockCarriageWork(ctx, tx, supplied.Scope, supplied.ID, supplied.ClaimToken)
	if err != nil {
		return false, err
	}
	err = carriageDispatchCurrent(ctx, tx, work, supplied, begin)
	if errors.Is(err, orders.ErrBlocked) || errors.Is(err, orders.ErrStale) || errors.Is(err, orders.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if begin {
		now, e := ordersClock(ctx, tx)
		if e != nil {
			return false, e
		}
		work.State, work.DispatchCount = orders.StateDispatching, 1
		if err = writeCarriageWork(ctx, tx, &work, now); err != nil {
			return false, err
		}
		if err = journalCarriageAttempt(ctx, tx, work); err != nil {
			return false, err
		}
	}
	return true, ordersRowsError("commit carriage dispatch guard", tx.Commit(ctx))
}

func carriageDispatchCurrent(ctx context.Context, tx pgx.Tx, work, supplied orders.CarriageWork, begin bool) error {
	if work.Plan.Fingerprint != supplied.Plan.Fingerprint || work.ConfigRevision != supplied.ConfigRevision || work.Credentials != supplied.Credentials || work.Plan.Validate() != nil {
		return orders.ErrStale
	}
	config, credentials, err := carriageCurrent(ctx, tx, work.Scope)
	if err != nil {
		return err
	}
	if credentials != work.Credentials {
		return orders.ErrStale
	}
	var owner string
	if err = tx.QueryRow(ctx, `SELECT owner_user_id FROM orders_carriage_work WHERE id=$1`, work.ID).Scan(&owner); err != nil {
		return ordersError("read carriage consenting owner", err)
	}
	if err = ordersOwnerCurrent(ctx, tx, work.Scope.Tenant, owner); err != nil {
		return err
	}
	if !begin && work.State.ReadOnly() && work.DispatchCount == 1 {
		return nil
	}
	if work.State != orders.StateClaimed || work.DispatchCount != 0 || config.Revision != work.ConfigRevision {
		return orders.ErrBlocked
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	if !work.Plan.ExpiresAt.After(now) {
		return orders.ErrStale
	}
	var head string
	err = tx.QueryRow(ctx, `SELECT work_id FROM orders_carriage_heads WHERE ozon_account=$1 AND delivery_method_id=$2`, work.Scope.OzonAccount, work.Plan.DeliveryMethodID).Scan(&head)
	if err != nil {
		return ordersError("read current carriage target", err)
	}
	if head != work.ID {
		return orders.ErrBlocked
	}
	return nil
}

func (s *Store) RequestCarriageReadback(ctx context.Context, scope orders.Scope, id, actor string) error {
	if scope.Validate() != nil || id == "" {
		return orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ordersError("begin explicit carriage reread", err)
	}
	defer rollbackMerchantMutation(tx)
	if _, err = ordersActor(ctx, tx, scope.Tenant, actor); err != nil {
		return err
	}
	work, err := readCarriageWork(ctx, tx, scope, id)
	if err != nil {
		return err
	}
	if !work.State.ReadOnly() || work.DispatchCount != 1 {
		return orders.ErrBlocked
	}
	if _, _, err = carriageCurrent(ctx, tx, scope); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE outbox_tasks SET available_at=clock_timestamp() WHERE task_type='orders_carriage' AND carriage_work_id=$1 AND state='pending'`, id)
	if err != nil {
		return ordersError("schedule carriage read only check", err)
	}
	return ordersRowsError("commit explicit carriage reread", tx.Commit(ctx))
}
