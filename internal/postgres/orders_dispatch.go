package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) GuardOrderDispatch(ctx context.Context, guard orders.DispatchGuard) (bool, error) {
	if guard.Work.Scope.Validate() != nil || guard.Work.ID == "" || guard.Work.ClaimToken == "" {
		return false, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, ordersError("begin order dispatch guard", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = lockOrderReviewScope(ctx, tx, guard.Work.Scope); err != nil {
		return false, err
	}
	work, err := lockOrderWork(ctx, tx, guard.Work.Scope, guard.Work.ID, guard.Work.ClaimToken)
	if err != nil {
		return false, err
	}
	valid, err := orderDispatchCurrent(ctx, tx, work, guard)
	if err != nil || !valid {
		return false, err
	}
	if guard.Begin {
		if err = beginOrderDispatch(ctx, tx, work); err != nil {
			return false, err
		}
	}
	return true, ordersRowsError("commit order dispatch guard", tx.Commit(ctx))
}

func orderDispatchCurrent(ctx context.Context, tx pgx.Tx, work orders.Work, guard orders.DispatchGuard) (bool, error) {
	hash, err := orders.CommandFingerprint(guard.Work.Command)
	if err != nil {
		return false, err
	}
	if hash != work.CommandFingerprint || guard.Work.CommandFingerprint != work.CommandFingerprint || guard.Work.ConfigRevision != work.ConfigRevision || guard.Work.Credentials != work.Credentials {
		return false, nil
	}
	if guard.Begin && (work.State != orders.StateClaimed || work.DispatchCount != 0) {
		return false, nil
	}
	if !guard.Begin && work.State != orders.StateClaimed && work.State != orders.StateDispatching && !work.State.ReadOnly() {
		return false, nil
	}
	current, err := readOrderWorkContext(ctx, tx, work)
	if err != nil {
		return false, err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return false, err
	}
	if err = validateOrderWorkAuthority(ctx, tx, work, current.Config); err != nil {
		return false, err
	}
	if current.Credentials != work.Credentials || current.Config.Revision != work.ConfigRevision {
		return false, nil
	}
	if work.State.ReadOnly() {
		return true, nil
	}
	return validateOrderUnsentWork(work, current, now)
}

func readOrderWorkContext(ctx context.Context, q ordersQuerier, work orders.Work) (orders.ReviewContext, error) {
	var result orders.ReviewContext
	var err error
	result.Config, err = readOrderConfig(ctx, q, work.Scope)
	if err != nil {
		return result, err
	}
	result.Credentials, err = ordersCredentials(ctx, q, work.Scope)
	if err != nil {
		return result, err
	}
	posting, err := readOrderPosting(ctx, q, work.Scope, work.Command.PostingNumber)
	if err != nil {
		return result, err
	}
	result.Postings = []orders.Posting{posting}
	result.ProductMappings, err = readOrderProductMappings(ctx, q, work.Scope)
	if err != nil {
		return result, err
	}
	result.WarehouseMappings, err = readOrderWarehouseMappings(ctx, q, work.Scope)
	if err != nil {
		return result, err
	}
	result.Correlations, err = readOrderCorrelations(ctx, q, work.Scope)
	if err != nil {
		return result, err
	}
	result.Metadata, err = readOrderMetadata(ctx, q, work.Scope)
	return result, err
}

func validateOrderWorkAuthority(ctx context.Context, tx pgx.Tx, work orders.Work, config orders.Config) error {
	var owner, actor string
	err := tx.QueryRow(ctx, `SELECT owner_user_id,actor FROM orders_operations WHERE id=$1 AND tenant_id=$2 AND moysklad_account=$3 AND ozon_account=$4`, work.OperationID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount).Scan(&owner, &actor)
	if err != nil {
		return ordersError("read order operation authority", err)
	}
	if work.Automatic {
		_, err = ordersGrantOwner(ctx, tx, config, actor)
		return err
	}
	return ordersOwnerCurrent(ctx, tx, work.Scope.Tenant, owner)
}

func validateOrderUnsentWork(work orders.Work, current orders.ReviewContext, now time.Time) (bool, error) {
	if !current.Config.Enabled || !current.Config.TestEnabled || len(current.Postings) != 1 {
		return false, nil
	}
	posting := current.Postings[0]
	checked := posting.Evidence.CheckedAt
	if checked.IsZero() {
		checked = posting.Evidence.ObservedAt
	}
	if ordersQuarantined(posting) || checked.After(now.Add(time.Minute)) || now.Sub(checked) > time.Duration(current.Config.SourceFreshnessSeconds)*time.Second {
		return false, nil
	}
	err := validateOrderCommandCurrent(work.Command, posting, current, work.Automatic, now)
	if errors.Is(err, orders.ErrStale) || errors.Is(err, orders.ErrBlocked) {
		return false, nil
	}
	return err == nil, err
}

func beginOrderDispatch(ctx context.Context, tx pgx.Tx, work orders.Work) error {
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	work.State, work.DispatchCount = orders.StateDispatching, 1
	raw, err := ordersJSON(work)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE orders_work SET state='DISPATCHING',work=$2,dispatch_count=1,dispatched_at=$3,updated_at=$3 WHERE id=$1`, work.ID, raw, now)
	if err != nil {
		return ordersError("fence exact order dispatch", err)
	}
	command, err := ordersJSON(work.Command)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_write_intents(work_id,tenant_id,moysklad_account,ozon_account,command,fingerprint,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, command, work.CommandFingerprint, now)
	if err != nil {
		return ordersError("journal immutable order write intent", err)
	}
	return insertOrderAttempt(ctx, tx, work, "", now)
}
