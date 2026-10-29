package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/providercall"
)

func (s *Store) StartProviderCall(ctx context.Context, lease providercall.Lease) (time.Duration, error) {
	a := lease.Attempt
	var delay time.Duration
	err := s.providerTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockProviderAccount(ctx, tx, a, false); err != nil {
			return err
		}
		now, err := providerClock(ctx, tx)
		if err != nil {
			return err
		}
		state, live, err := lockProviderPermit(ctx, tx, a, now)
		if err != nil {
			return err
		}
		if state == "sending" {
			return providercall.ErrAlreadyStarted
		}
		if state != "reserved" || !live {
			return providercall.ErrLeaseLost
		}
		if err = validateProviderCredential(ctx, tx, a); err != nil {
			return err
		}
		delay, err = providerDelay(ctx, tx, a, now, false)
		if err != nil || delay > 0 {
			return err
		}
		return markProviderSending(ctx, tx, a, now)
	})
	return delay, err
}

func lockProviderPermit(ctx context.Context, tx pgx.Tx, a providercall.Attempt, now time.Time) (string, bool, error) {
	var state string
	var live bool
	err := tx.QueryRow(ctx, `SELECT state,lease_until>$9 FROM provider_call_permits WHERE id=$1 AND provider=$2 AND account_id=$3 AND tenant_id=$4 AND credential_revision=$5 AND method_key=$6 AND operation_key=$7 AND target_key=$8 AND is_write=$10 FOR UPDATE`, a.ID, a.Scope.Provider, a.Scope.Account, a.Scope.Tenant, a.Scope.CredentialRevision, a.Method, a.Operation, a.TargetKey, now, a.IsWrite).Scan(&state, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, providercall.ErrLeaseLost
	}
	if err != nil {
		return "", false, fmt.Errorf("lock provider permit: %w", err)
	}
	return state, live, nil
}

func markProviderSending(ctx context.Context, tx pgx.Tx, a providercall.Attempt, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE provider_call_accounts SET attempts=attempts+1,next_send_at=$3::timestamptz+(interval_ns::double precision/1000000000)*interval '1 second',updated_at=$3 WHERE provider=$1 AND account_id=$2`, a.Scope.Provider, a.Scope.Account, now)
	if err != nil {
		return fmt.Errorf("record provider attempt: %w", err)
	}
	_, err = tx.Exec(ctx, `UPDATE provider_call_methods SET attempts=attempts+1 WHERE provider=$1 AND account_id=$2 AND method_key=$3`, a.Scope.Provider, a.Scope.Account, a.Method)
	if err != nil {
		return fmt.Errorf("record provider method attempt: %w", err)
	}
	if a.TargetKey != "" {
		_, err = tx.Exec(ctx, `UPDATE provider_call_targets SET next_send_at=$4 WHERE provider=$1 AND account_id=$2 AND target_key=$3`, a.Scope.Provider, a.Scope.Account, a.TargetKey, now.Add(a.TargetInterval))
		if err != nil {
			return fmt.Errorf("record provider target attempt: %w", err)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE provider_call_permits SET state='sending',started_at=$2 WHERE id=$1`, a.ID, now)
	if err != nil {
		return fmt.Errorf("mark provider permit sending: %w", err)
	}
	return nil
}
