package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) ClaimOrderWork(ctx context.Context, req orders.ClaimRequest) (*orders.Work, error) {
	duration := req.LeaseUntil.Sub(req.Now)
	if !ordersRequestKey(req.WorkerID) || duration <= 0 || duration > 5*time.Minute {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, ordersError("begin order work claim", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = expireOrderDispatches(ctx, tx); err != nil {
		return nil, err
	}
	work, err := claimOrderWorkLocked(ctx, tx, duration)
	if err != nil {
		return nil, err
	}
	return work, ordersRowsError("commit order work claim", tx.Commit(ctx))
}

func claimOrderWorkLocked(ctx context.Context, tx pgx.Tx, duration time.Duration) (*orders.Work, error) {
	var raw []byte
	var taskID int64
	err := tx.QueryRow(ctx, `SELECT q.id,w.work FROM outbox_tasks q JOIN orders_work w ON w.id=q.orders_work_id WHERE q.task_type='orders_sync' AND ((q.state='pending' AND q.available_at<=clock_timestamp()) OR (q.state='running' AND q.lease_until<=clock_timestamp())) AND w.state IN ('QUEUED','CLAIMED','ACKNOWLEDGED','POLLING','UNKNOWN') ORDER BY q.available_at,q.id LIMIT 1 FOR UPDATE OF q,w SKIP LOCKED`).Scan(&taskID, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ordersError("claim order outbox task", err)
	}
	var work orders.Work
	if err = ordersDecode(raw, &work); err != nil {
		return nil, err
	}
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
	if err = writeOrderWork(ctx, tx, work, now); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE outbox_tasks SET state='running',attempts=1,claim_token=$2,lease_until=$3,updated_at=$4 WHERE id=$1`, taskID, work.ClaimToken, work.LeaseUntil, now)
	return &work, ordersRowsError("fence order worker claim", err)
}

func expireOrderDispatches(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT w.work FROM outbox_tasks q JOIN orders_work w ON w.id=q.orders_work_id WHERE q.task_type='orders_sync' AND q.state='running' AND q.lease_until<=clock_timestamp() AND w.state='DISPATCHING' ORDER BY q.id LIMIT 100 FOR UPDATE OF q,w SKIP LOCKED`)
	if err != nil {
		return ordersError("select expired order dispatches", err)
	}
	works := make([]orders.Work, 0, 100)
	for rows.Next() {
		var raw []byte
		var work orders.Work
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return ordersError("scan expired order dispatch", err)
		}
		if err = ordersDecode(raw, &work); err != nil {
			rows.Close()
			return err
		}
		works = append(works, work)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return ordersError("read expired order dispatches", err)
	}
	for _, work := range works {
		if err = expireOrderDispatch(ctx, tx, work); err != nil {
			return err
		}
	}
	return nil
}

func expireOrderDispatch(ctx context.Context, tx pgx.Tx, work orders.Work) error {
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	work.State, work.Code = orders.StateUnknown, "DISPATCH_LEASE_EXPIRED"
	work.LeaseUntil = time.Time{}
	work.NextRunAt = now
	if err = writeOrderWork(ctx, tx, work, now); err != nil {
		return err
	}
	if err = insertOrderAttempt(ctx, tx, work, work.Code, now); err != nil {
		return err
	}
	return finishOrderTask(ctx, tx, work, now)
}

func lockOrderWork(ctx context.Context, tx pgx.Tx, scope orders.Scope, id, token string) (orders.Work, error) {
	var raw []byte
	var taskState, taskToken string
	var until time.Time
	args := append(ordersScopeArgs(scope), id)
	err := tx.QueryRow(ctx, `SELECT w.work,q.state,COALESCE(q.claim_token,''),COALESCE(q.lease_until,'epoch'::timestamptz) FROM orders_work w JOIN outbox_tasks q ON q.orders_work_id=w.id AND q.task_type='orders_sync' WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.id=$4 FOR UPDATE OF q,w`, args...).Scan(&raw, &taskState, &taskToken, &until)
	if err != nil {
		return orders.Work{}, ordersError("lock order work claim", err)
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return orders.Work{}, err
	}
	if token == "" || token != taskToken || taskState != "running" || !until.After(now) {
		return orders.Work{}, orders.ErrStale
	}
	var work orders.Work
	if err = ordersDecode(raw, &work); err != nil {
		return work, err
	}
	work.ClaimToken, work.LeaseUntil = token, until
	return work, nil
}

func writeOrderWork(ctx context.Context, tx pgx.Tx, work orders.Work, now time.Time) error {
	raw, err := ordersJSON(work)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE orders_work SET state=$2,work=$3,dispatch_count=$4,updated_at=$5,finished_at=CASE WHEN $6 THEN $5 ELSE finished_at END WHERE id=$1`, work.ID, work.State, raw, work.DispatchCount, now, work.State.Terminal())
	return ordersRowsError("save order work phase", err)
}

func insertOrderAttempt(ctx context.Context, tx pgx.Tx, work orders.Work, code string, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO orders_attempts(work_id,tenant_id,moysklad_account,ozon_account,phase,code,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, work.State, ordersSafeCode(code), now)
	return ordersRowsError("journal order work attempt", err)
}

func finishOrderTask(ctx context.Context, tx pgx.Tx, work orders.Work, now time.Time) error {
	state := "pending"
	if work.State.Terminal() {
		state = "failed"
	}
	if work.State == orders.StateReconciled {
		state = "succeeded"
	}
	next := work.NextRunAt
	if next.Before(now) {
		next = now
	}
	_, err := tx.Exec(ctx, `UPDATE outbox_tasks SET state=$2,available_at=$3,claim_token=NULL,lease_until=NULL,last_error=NULLIF($4,''),updated_at=$5 WHERE task_type='orders_sync' AND orders_work_id=$1`, work.ID, state, next, ordersSafeCode(work.Code), now)
	return ordersRowsError("finish order outbox phase", err)
}
