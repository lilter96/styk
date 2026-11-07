package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) ClaimCommissionWork(ctx context.Context, req orders.ClaimRequest) (*orders.CommissionWork, error) {
	duration := req.LeaseUntil.Sub(req.Now)
	if !ordersRequestKey(req.WorkerID) || duration <= 0 || duration > 5*time.Minute {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, ordersError("begin commission outbox claim", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = expireCommissionDispatches(ctx, tx); err != nil {
		return nil, err
	}
	work, err := claimCommissionLocked(ctx, tx, duration)
	if err != nil {
		return nil, err
	}
	return work, ordersRowsError("commit commission outbox claim", tx.Commit(ctx))
}

func claimCommissionLocked(ctx context.Context, tx pgx.Tx, duration time.Duration) (*orders.CommissionWork, error) {
	var taskID int64
	var raw []byte
	var changed bool
	err := tx.QueryRow(ctx, `SELECT q.id,w.work,COALESCE(p.source_changed,false) FROM outbox_tasks q JOIN orders_finance_work w ON w.id=q.finance_work_id LEFT JOIN orders_finance_periods p ON p.work_id=w.id WHERE q.task_type='orders_finance' AND ((q.state='pending' AND q.available_at<=clock_timestamp()) OR(q.state='running' AND q.lease_until<=clock_timestamp())) AND w.state IN('QUEUED','CLAIMED','ACKNOWLEDGED','POLLING','UNKNOWN') ORDER BY q.available_at,q.id LIMIT 1 FOR UPDATE OF q,w SKIP LOCKED`).Scan(&taskID, &raw, &changed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ordersError("claim commission outbox row", err)
	}
	var work orders.CommissionWork
	if err = ordersDecode(raw, &work); err != nil {
		return nil, err
	}
	work.SourceChanged = work.SourceChanged || changed
	work.ClaimToken, err = ordersID()
	if err != nil {
		return nil, err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return nil, err
	}
	work.LeaseUntil = now.Add(duration)
	if work.State == orders.StateQueued {
		work.State = orders.StateClaimed
	}
	if err = writeCommissionWork(ctx, tx, &work, now); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE outbox_tasks SET state='running',attempts=1,claim_token=$2,lease_until=$3,updated_at=$4 WHERE id=$1`, taskID, work.ClaimToken, work.LeaseUntil, now)
	return &work, ordersRowsError("fence commission outbox claimant", err)
}

func expireCommissionDispatches(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT w.work FROM outbox_tasks q JOIN orders_finance_work w ON w.id=q.finance_work_id WHERE q.task_type='orders_finance' AND q.state='running' AND q.lease_until<=clock_timestamp() AND w.state='DISPATCHING' ORDER BY q.id LIMIT 100 FOR UPDATE OF q,w SKIP LOCKED`)
	if err != nil {
		return ordersError("read expired commission attempts", err)
	}
	works := make([]orders.CommissionWork, 0, 100)
	for rows.Next() {
		var raw []byte
		var work orders.CommissionWork
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return ordersError("scan expired commission attempt", err)
		}
		if err = ordersDecode(raw, &work); err != nil {
			rows.Close()
			return err
		}
		works = append(works, work)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return ordersError("read expired commission attempts", err)
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	for _, work := range works {
		if err = expireCommissionWork(ctx, tx, work, now); err != nil {
			return err
		}
	}
	return nil
}

func expireCommissionWork(ctx context.Context, tx pgx.Tx, work orders.CommissionWork, now time.Time) error {
	work.State, work.Code, work.NextRunAt = orders.StateUnknown, "FINANCE_DISPATCH_LEASE_EXPIRED", now
	if err := writeCommissionWork(ctx, tx, &work, now); err != nil {
		return err
	}
	if err := insertCommissionAttempt(ctx, tx, work, nil); err != nil {
		return err
	}
	return finishCommissionTask(ctx, tx, work)
}

func lockCommissionWork(ctx context.Context, tx pgx.Tx, scope orders.Scope, id, token string) (orders.CommissionWork, error) {
	var work orders.CommissionWork
	var raw []byte
	var state, actualToken string
	err := tx.QueryRow(ctx, `SELECT w.work,q.state,COALESCE(q.claim_token,''),COALESCE(q.lease_until,'epoch'::timestamptz) FROM orders_finance_work w JOIN outbox_tasks q ON q.finance_work_id=w.id AND q.task_type='orders_finance' WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.id=$4 FOR UPDATE OF q,w`, append(ordersScopeArgs(scope), id)...).Scan(&raw, &state, &actualToken, &work.LeaseUntil)
	if err != nil {
		return work, ordersError("lock commission work claim", err)
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return work, err
	}
	if token == "" || token != actualToken || state != "running" || !work.LeaseUntil.After(now) {
		return work, orders.ErrStale
	}
	lease := work.LeaseUntil
	if err = ordersDecode(raw, &work); err != nil {
		return work, err
	}
	work.ClaimToken, work.LeaseUntil = token, lease
	return work, nil
}

func writeCommissionWork(ctx context.Context, tx pgx.Tx, work *orders.CommissionWork, now time.Time) error {
	work.UpdatedAt = now
	raw, err := ordersJSON(work)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE orders_finance_work SET state=$2,dispatch_count=$3,work=$4,updated_at=$5 WHERE id=$1`, work.ID, work.State, work.DispatchCount, raw, now)
	return ordersRowsError("record commission work phase", err)
}

func finishCommissionTask(ctx context.Context, tx pgx.Tx, work orders.CommissionWork) error {
	state := "pending"
	if work.State.Terminal() {
		state = "succeeded"
	}
	_, err := tx.Exec(ctx, `UPDATE outbox_tasks SET state=$2,claim_token=NULL,lease_until=NULL,available_at=$3,updated_at=clock_timestamp() WHERE task_type='orders_finance' AND finance_work_id=$1`, work.ID, state, work.NextRunAt)
	return ordersRowsError("schedule commission readback or finish", err)
}

func insertCommissionAttempt(ctx context.Context, tx pgx.Tx, work orders.CommissionWork, observation *orders.CommissionObservation) error {
	receipt, err := ordersJSON(work.Receipt)
	if err != nil {
		return err
	}
	var actual any
	if observation != nil {
		raw, e := ordersJSON(observation)
		if e != nil {
			return e
		}
		actual = raw
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_finance_attempts(work_id,phase,code,receipt,observation) VALUES($1,$2,$3,$4,$5)`, work.ID, work.State, ordersSafeCode(work.Code), receipt, actual)
	return ordersRowsError("journal commission attempt/readback", err)
}
