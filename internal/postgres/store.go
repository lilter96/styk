package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lilter96/styk/internal/domain"
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
	return s.claim(ctx, lease, "", "", true, false)
}

// ClaimForWorker dispatches an import only to a worker configured for the
// same Ozon account; mock tasks remain available to every worker.
func (s *Store) ClaimForWorker(ctx context.Context, lease time.Duration, account string) (*domain.Task, error) {
	return s.claim(ctx, lease, account, "", false, false)
}
func (s *Store) ClaimForCatalogWorker(ctx context.Context, lease time.Duration, ozonAccount, moyskladAccount string) (*domain.Task, error) {
	return s.claim(ctx, lease, ozonAccount, moyskladAccount, false, false)
}
func (s *Store) ClaimForCredentialWorker(ctx context.Context, lease time.Duration, ozonAccount, moyskladAccount string) (*domain.Task, error) {
	return s.claim(ctx, lease, ozonAccount, moyskladAccount, false, true)
}
func (s *Store) claim(ctx context.Context, lease time.Duration, account, moyskladAccount string, all, stored bool) (*domain.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("claim token: %w", err)
	}
	token := hex.EncodeToString(b[:])
	var t domain.Task
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin task claim: %w", err)
	}
	defer tx.Rollback(context.Background())
	// An expired read-only import may be traversed again; only its fenced
	// persistence transaction may publish a batch or a terminal result.
	storedPredicate := ""
	if stored {
		storedPredicate = ` OR (task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND EXISTS(SELECT 1 FROM ozon_import_operations op JOIN connection_credentials cc ON cc.tenant_id=op.tenant_id AND cc.provider=op.provider AND cc.account_id=op.account_id AND cc.enabled WHERE op.id=outbox_tasks.import_operation_id))`
	}
	err = tx.QueryRow(ctx, `WITH candidate AS (SELECT id FROM outbox_tasks WHERE task_type<>'stock_publication' AND (state='pending' AND available_at<=clock_timestamp() OR state='running' AND lease_until<=clock_timestamp()) AND attempts<max_attempts AND ($3 OR task_type='mock' OR (task_type='ozon_catalog_import' AND import_account_id=$4) OR (task_type='moysklad_catalog_import' AND import_account_id=$5)`+storedPredicate+`) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE outbox_tasks t SET state='running', attempts=t.attempts+1, claim_token=$1, lease_until=clock_timestamp()+$2::interval, updated_at=clock_timestamp() FROM candidate WHERE t.id=candidate.id RETURNING t.id,t.operation_key,COALESCE(t.observation_id,0),t.attempts,t.max_attempts,t.claim_token,t.task_type,COALESCE(t.import_operation_id,'')`, token, lease.String(), all, account, moyskladAccount).Scan(&t.ID, &t.OperationKey, &t.ObservationID, &t.Attempts, &t.MaxAttempts, &t.ClaimToken, &t.Type, &t.ImportOperationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim task: %w", err)
	}
	if t.Type == domain.OzonCatalogTask || t.Type == domain.MoyskladCatalogTask {
		if _, err = tx.Exec(ctx, `UPDATE ozon_import_operations SET started_at=COALESCE(started_at,clock_timestamp()),attempt_started_at=clock_timestamp(),progress_at=clock_timestamp(),phase='READING_PRODUCTS',counters='{}'::jsonb WHERE id=$1`, t.ImportOperationID); err != nil {
			return nil, fmt.Errorf("start import attempt: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit task claim: %w", err)
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
	tag, err := s.Pool.Exec(ctx, `UPDATE outbox_tasks SET state=$3,result=NULLIF($4,''),last_error=NULLIF($5,''),available_at=now()+$6::interval,lease_until=NULL,claim_token=NULL,updated_at=now() WHERE id=$1 AND task_type='mock' AND state='running' AND claim_token=$2`, t.ID, t.ClaimToken, state, result, msg, delay.String())
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
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin task expiry: %w", err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `UPDATE outbox_tasks SET state='failed',last_error='final mock attempt lease expired',claim_token=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE task_type='mock' AND state='running' AND lease_until<=clock_timestamp() AND attempts>=max_attempts`)
	if err != nil {
		return fmt.Errorf("expire tasks: %w", err)
	}
	_, err = tx.Exec(ctx, `WITH expired AS (UPDATE outbox_tasks SET state='failed',last_error='LEASE_EXHAUSTED',claim_token=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND state='running' AND lease_until<=clock_timestamp() AND attempts>=max_attempts RETURNING import_operation_id) UPDATE ozon_import_operations o SET phase='FAILED',outcome='LEASE_EXHAUSTED',finished_at=clock_timestamp() FROM expired e WHERE o.id=e.import_operation_id`)
	if err != nil {
		return fmt.Errorf("expire import tasks: %w", err)
	}
	return tx.Commit(ctx)
}

// RenewImportClaim never resurrects an expired or replaced lease.
func (s *Store) RenewImportClaim(ctx context.Context, t domain.Task, lease time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tag, err := s.Pool.Exec(ctx, `UPDATE outbox_tasks SET lease_until=clock_timestamp()+$3::interval,updated_at=clock_timestamp() WHERE id=$1 AND claim_token=$2 AND task_type IN ('ozon_catalog_import','moysklad_catalog_import') AND state='running' AND lease_until>clock_timestamp()`, t.ID, t.ClaimToken, lease.String())
	if err != nil {
		return fmt.Errorf("renew import claim: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrImportClaimLost
	}
	return nil
}
