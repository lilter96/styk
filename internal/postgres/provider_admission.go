package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/providercall"
)

func (s *Store) TryAcquireProviderCall(ctx context.Context, a providercall.Attempt) (providercall.Admission, error) {
	var admitted providercall.Admission
	err := s.providerTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockProviderAccount(ctx, tx, a, true); err != nil {
			return err
		}
		if err := lockProviderDimensions(ctx, tx, a); err != nil {
			return err
		}
		now, err := providerClock(ctx, tx)
		if err != nil {
			return err
		}
		if err = validateProviderCredential(ctx, tx, a); err != nil {
			return err
		}
		if err = expireProviderPermits(ctx, tx, a, now); err != nil {
			return err
		}
		admitted.Delay, err = providerDelay(ctx, tx, a, now, true)
		if err != nil || admitted.Delay > 0 {
			return err
		}
		if err = reserveProviderPermit(ctx, tx, a, now); err != nil {
			return err
		}
		admitted.Lease = providercall.Lease{Attempt: a}
		return nil
	})
	return admitted, err
}

func expireProviderPermits(ctx context.Context, tx pgx.Tx, a providercall.Attempt, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE provider_call_permits SET state='expired',finished_at=$3 WHERE provider=$1 AND account_id=$2 AND state IN ('reserved','sending') AND lease_until<=$3`, a.Scope.Provider, a.Scope.Account, now)
	if err != nil {
		return fmt.Errorf("expire provider permits: %w", err)
	}
	// Coordination records are bounded. Application mutation intents live in
	// their own durable journals and are neither retried nor removed here.
	_, err = tx.Exec(ctx, `DELETE FROM provider_call_permits WHERE id IN (SELECT id FROM provider_call_permits WHERE provider=$1 AND account_id=$2 AND state IN ('finished','aborted','expired') ORDER BY finished_at DESC,id DESC OFFSET 1000)`, a.Scope.Provider, a.Scope.Account)
	if err != nil {
		return fmt.Errorf("prune provider permit history: %w", err)
	}
	return nil
}

func providerDelay(ctx context.Context, tx pgx.Tx, a providercall.Attempt, now time.Time, inflight bool) (time.Duration, error) {
	var blocked bool
	var seconds, occupied float64
	err := tx.QueryRow(ctx, `SELECT a.cooldown_blocked,GREATEST(0,EXTRACT(epoch FROM GREATEST(a.next_send_at,a.cooldown_until,m.cooldown_until,COALESCE(t.next_send_at,'-infinity'::timestamptz))-$5))::double precision,CASE WHEN $6 AND (SELECT count(*) FROM provider_call_permits p WHERE p.provider=a.provider AND p.account_id=a.account_id AND p.state IN ('reserved','sending') AND p.lease_until>$5)>=a.max_inflight THEN LEAST(0.25,GREATEST(0,EXTRACT(epoch FROM (SELECT min(lease_until) FROM provider_call_permits p WHERE p.provider=a.provider AND p.account_id=a.account_id AND p.state IN ('reserved','sending') AND p.lease_until>$5)-$5)))::double precision ELSE 0 END FROM provider_call_accounts a JOIN provider_call_methods m ON m.provider=a.provider AND m.account_id=a.account_id AND m.method_key=$3 LEFT JOIN provider_call_targets t ON t.provider=a.provider AND t.account_id=a.account_id AND t.target_key=$4 WHERE a.provider=$1 AND a.account_id=$2`, a.Scope.Provider, a.Scope.Account, a.Method, a.TargetKey, now, inflight).Scan(&blocked, &seconds, &occupied)
	if err != nil {
		return 0, fmt.Errorf("read provider admission delay: %w", err)
	}
	if blocked {
		return 0, providercall.ErrCooldownUnbounded
	}
	seconds = max(seconds, occupied)
	if seconds <= 0 {
		return 0, nil
	}
	// Poll occupied slots so an early completion does not strand a waiter until
	// the full lease expires. Every iteration releases its database connection.
	delay := time.Duration(seconds * float64(time.Second))
	return max(delay, time.Microsecond), nil
}

func reserveProviderPermit(ctx context.Context, tx pgx.Tx, a providercall.Attempt, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO provider_call_permits(id,provider,account_id,tenant_id,credential_revision,method_key,operation_key,target_key,is_write,state,created_at,lease_until) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'reserved',$10,$11)`, a.ID, a.Scope.Provider, a.Scope.Account, a.Scope.Tenant, a.Scope.CredentialRevision, a.Method, a.Operation, a.TargetKey, a.IsWrite, now, now.Add(a.LeaseDuration))
	if err != nil {
		return fmt.Errorf("reserve provider permit: %w", err)
	}
	return nil
}
