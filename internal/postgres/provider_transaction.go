package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/providercall"
)

func (s *Store) providerTransaction(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin provider coordination: %w", err)
	}
	defer rollbackTransaction(tx)
	if err = fn(ctx, tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit provider coordination: %w", err)
	}
	return nil
}

func lockProviderAccount(ctx context.Context, tx pgx.Tx, a providercall.Attempt, create bool) error {
	if create {
		_, err := tx.Exec(ctx, `INSERT INTO provider_call_accounts(provider,account_id,interval_ns,max_inflight) VALUES($1,$2,$3,$4) ON CONFLICT(provider,account_id) DO UPDATE SET next_send_at=provider_call_accounts.next_send_at+GREATEST(0,EXCLUDED.interval_ns-provider_call_accounts.interval_ns)::double precision/1000000000*interval '1 second',interval_ns=GREATEST(provider_call_accounts.interval_ns,EXCLUDED.interval_ns),max_inflight=LEAST(provider_call_accounts.max_inflight,EXCLUDED.max_inflight)`, a.Scope.Provider, a.Scope.Account, int64(a.Interval), a.MaxInflight)
		if err != nil {
			return fmt.Errorf("register provider budget: %w", err)
		}
	}
	var provider string
	err := tx.QueryRow(ctx, `SELECT provider FROM provider_call_accounts WHERE provider=$1 AND account_id=$2 FOR UPDATE`, a.Scope.Provider, a.Scope.Account).Scan(&provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return providercall.ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("lock provider budget: %w", err)
	}
	return nil
}

func lockProviderDimensions(ctx context.Context, tx pgx.Tx, a providercall.Attempt) error {
	_, err := tx.Exec(ctx, `INSERT INTO provider_call_methods(provider,account_id,method_key) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, a.Scope.Provider, a.Scope.Account, a.Method)
	if err != nil {
		return fmt.Errorf("register provider method: %w", err)
	}
	if a.TargetKey == "" {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO provider_call_targets(provider,account_id,target_key) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, a.Scope.Provider, a.Scope.Account, a.TargetKey)
	if err != nil {
		return fmt.Errorf("register provider target: %w", err)
	}
	return nil
}

// Account serialization precedes the clock read and every dependent decision.
func providerClock(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return now, fmt.Errorf("read provider coordination clock: %w", err)
	}
	return now, nil
}

func validateProviderCredential(ctx context.Context, tx pgx.Tx, a providercall.Attempt) error {
	var rejected, current bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_call_rejected_credentials WHERE provider=$1 AND account_id=$2 AND tenant_id=$3 AND credential_revision=$4),($4::bigint=0 OR EXISTS(SELECT 1 FROM connection_credentials c JOIN stock_connections s ON s.tenant_id=c.tenant_id AND s.source=c.provider AND s.account_id=c.account_id WHERE c.provider=$1 AND c.account_id=$2 AND c.tenant_id=$3 AND c.revision=$4 AND c.enabled))`, a.Scope.Provider, a.Scope.Account, a.Scope.Tenant, a.Scope.CredentialRevision).Scan(&rejected, &current)
	if err != nil {
		return fmt.Errorf("validate provider credential revision: %w", err)
	}
	if rejected || !current {
		return providercall.ErrCredentialsRejected
	}
	return nil
}
