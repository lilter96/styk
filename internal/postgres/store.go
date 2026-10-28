package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"example.com/styk/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool    *pgxpool.Pool
	Timeout time.Duration
}

func Open(ctx context.Context, url string, timeout time.Duration) (*Store, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	return &Store{p, timeout}, nil
}
func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	return s.Pool.Ping(ctx)
}

// Submit commits the observation and its task together. A reused key must have identical content.
func (s *Store) Submit(ctx context.Context, o domain.Observation, max int) (int64, error) {
	if err := o.Validate(); err != nil {
		return 0, err
	}
	if max < 1 {
		return 0, fmt.Errorf("max attempts must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin submission: %w", err)
	}
	defer tx.Rollback(context.Background())
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO inventory_observations(operation_key,source,sku,quantity) VALUES($1,$2,$3,$4) ON CONFLICT (operation_key) DO UPDATE SET operation_key=EXCLUDED.operation_key WHERE inventory_observations.source=EXCLUDED.source AND inventory_observations.sku=EXCLUDED.sku AND inventory_observations.quantity=EXCLUDED.quantity RETURNING id`, o.OperationKey, o.Source, o.SKU, o.Quantity).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("operation key already used for a different observation")
	}
	if err != nil {
		return 0, fmt.Errorf("save observation: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_tasks(operation_key,observation_id,max_attempts) VALUES($1,$2,$3) ON CONFLICT(operation_key) DO NOTHING`, o.OperationKey, id, max)
	if err != nil {
		return 0, fmt.Errorf("save outbox task: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit submission: %w", err)
	}
	return id, nil
}
func (s *Store) Claim(ctx context.Context, lease time.Duration) (*domain.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("claim token: %w", err)
	}
	token := hex.EncodeToString(b[:])
	var t domain.Task
	// Expired mock work may be repeated; the token fences stale completions.
	err := s.Pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM outbox_tasks WHERE (state='pending' AND available_at<=now() OR state='running' AND lease_until<=now()) AND attempts<max_attempts ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE outbox_tasks t SET state='running', attempts=t.attempts+1, claim_token=$1, lease_until=now()+$2::interval, updated_at=now() FROM candidate WHERE t.id=candidate.id RETURNING t.id,t.operation_key,t.observation_id,t.attempts,t.max_attempts,t.claim_token`, token, lease.String()).Scan(&t.ID, &t.OperationKey, &t.ObservationID, &t.Attempts, &t.MaxAttempts, &t.ClaimToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}
	return &t, nil
}
func (s *Store) Finish(ctx context.Context, t domain.Task, result string, processErr error, delay time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	state := domain.Succeeded
	msg := ""
	if processErr != nil {
		state = domain.FailureState(t.Attempts, t.MaxAttempts)
		msg = processErr.Error()
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE outbox_tasks SET state=$3,result=NULLIF($4,''),last_error=NULLIF($5,''),available_at=now()+$6::interval,lease_until=NULL,claim_token=NULL,updated_at=now() WHERE id=$1 AND state='running' AND claim_token=$2`, t.ID, t.ClaimToken, state, result, msg, delay.String())
	if err != nil {
		return fmt.Errorf("finish task: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("task %d claim no longer owned", t.ID)
	}
	return nil
}

// Exhausted leases are terminal because their final attempt may have died before completion.
func (s *Store) ExpireExhausted(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	_, err := s.Pool.Exec(ctx, `UPDATE outbox_tasks SET state='failed',last_error='final mock attempt lease expired',claim_token=NULL,lease_until=NULL,updated_at=now() WHERE state='running' AND lease_until<=now() AND attempts>=max_attempts`)
	if err != nil {
		return fmt.Errorf("expire tasks: %w", err)
	}
	return nil
}
