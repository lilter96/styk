package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func (s *Store) ClaimCarriageWork(ctx context.Context, req orders.ClaimRequest) (*orders.CarriageWork, error) {
	duration := req.LeaseUntil.Sub(req.Now)
	if !ordersRequestKey(req.WorkerID) || duration <= 0 || duration > 5*time.Minute {
		return nil, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, ordersError("begin carriage claim", err)
	}
	defer rollbackMerchantMutation(tx)
	if err = expireCarriageAttempts(ctx, tx); err != nil {
		return nil, err
	}
	work, err := claimCarriageLocked(ctx, tx, duration)
	if err != nil {
		return nil, err
	}
	return work, ordersRowsError("commit carriage claim", tx.Commit(ctx))
}

func claimCarriageLocked(ctx context.Context, tx pgx.Tx, duration time.Duration) (*orders.CarriageWork, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT w.work FROM outbox_tasks q JOIN orders_carriage_work w ON w.id=q.carriage_work_id WHERE q.task_type='orders_carriage' AND((q.state='pending' AND q.available_at<=clock_timestamp()) OR(q.state='running' AND q.lease_until<=clock_timestamp())) AND w.state IN('QUEUED','CLAIMED','ACKNOWLEDGED','POLLING','UNKNOWN') ORDER BY q.available_at,q.id LIMIT 1 FOR UPDATE OF q,w SKIP LOCKED`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ordersError("claim carriage outbox", err)
	}
	var work orders.CarriageWork
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
	if err = writeCarriageWork(ctx, tx, &work, now); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE outbox_tasks SET state='running',attempts=1,claim_token=$2,lease_until=$3,updated_at=$4 WHERE task_type='orders_carriage' AND carriage_work_id=$1`, work.ID, work.ClaimToken, work.LeaseUntil, now)
	return &work, ordersRowsError("fence carriage claimant", err)
}

func expireCarriageAttempts(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT w.work FROM outbox_tasks q JOIN orders_carriage_work w ON w.id=q.carriage_work_id WHERE q.task_type='orders_carriage' AND q.state='running' AND q.lease_until<=clock_timestamp() AND w.state='DISPATCHING' ORDER BY q.id LIMIT 100 FOR UPDATE OF q,w SKIP LOCKED`)
	if err != nil {
		return ordersError("read expired carriage attempts", err)
	}
	works := make([]orders.CarriageWork, 0, 100)
	for rows.Next() {
		var raw []byte
		var work orders.CarriageWork
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return ordersError("scan carriage expiry", err)
		}
		if err = ordersDecode(raw, &work); err != nil {
			rows.Close()
			return err
		}
		works = append(works, work)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return ordersError("read carriage expiry", err)
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return err
	}
	for _, work := range works {
		work.State, work.Code, work.NextRunAt = orders.StateUnknown, "CARRIAGE_DISPATCH_LEASE_EXPIRED", now
		if err = writeCarriageWork(ctx, tx, &work, now); err != nil {
			return err
		}
		if err = journalCarriageAttempt(ctx, tx, work); err != nil {
			return err
		}
		if err = finishCarriageTask(ctx, tx, work); err != nil {
			return err
		}
	}
	return nil
}

func lockCarriageWork(ctx context.Context, tx pgx.Tx, scope orders.Scope, id, token string) (orders.CarriageWork, error) {
	var work orders.CarriageWork
	var raw []byte
	var state, actualToken string
	var lease time.Time
	err := tx.QueryRow(ctx, `SELECT w.work,q.state,COALESCE(q.claim_token,''),COALESCE(q.lease_until,'epoch'::timestamptz) FROM orders_carriage_work w JOIN outbox_tasks q ON q.carriage_work_id=w.id AND q.task_type='orders_carriage' WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3 AND w.id=$4 FOR UPDATE OF q,w`, append(ordersScopeArgs(scope), id)...).Scan(&raw, &state, &actualToken, &lease)
	if err != nil {
		return work, ordersError("lock carriage lease", err)
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return work, err
	}
	if token == "" || token != actualToken || state != "running" || !lease.After(now) {
		return work, orders.ErrStale
	}
	if err = ordersDecode(raw, &work); err != nil {
		return work, err
	}
	work.ClaimToken, work.LeaseUntil = token, lease
	return work, nil
}

func writeCarriageWork(ctx context.Context, tx pgx.Tx, work *orders.CarriageWork, now time.Time) error {
	work.UpdatedAt = now
	raw, err := ordersJSON(work)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE orders_carriage_work SET state=$2,dispatch_count=$3,carriage_id=$4,work=$5,updated_at=$6 WHERE id=$1`, work.ID, work.State, work.DispatchCount, work.CarriageID, raw, now)
	return ordersRowsError("record carriage phase", err)
}

func finishCarriageTask(ctx context.Context, tx pgx.Tx, work orders.CarriageWork) error {
	state := "pending"
	if work.State.Terminal() {
		state = "succeeded"
	}
	_, err := tx.Exec(ctx, `UPDATE outbox_tasks SET state=$2,claim_token=NULL,lease_until=NULL,available_at=$3,updated_at=clock_timestamp() WHERE task_type='orders_carriage' AND carriage_work_id=$1`, work.ID, state, work.NextRunAt)
	return ordersRowsError("finish or schedule carriage readback", err)
}

func journalCarriageAttempt(ctx context.Context, tx pgx.Tx, work orders.CarriageWork) error {
	receipt, err := ordersJSON(work.Receipt)
	if err != nil {
		return err
	}
	var observation any
	if work.Observation != nil {
		raw, e := ordersJSON(work.Observation)
		if e != nil {
			return e
		}
		observation = raw
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_carriage_attempts(work_id,phase,code,receipt,observation) VALUES($1,$2,$3,$4,$5)`, work.ID, work.State, ordersSafeCode(work.Code), receipt, observation)
	return ordersRowsError("journal carriage attempt", err)
}
