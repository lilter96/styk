package postgres

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/providercall"
)

func (s *Store) FinishProviderCall(ctx context.Context, lease providercall.Lease, result providercall.Result) error {
	a := lease.Attempt
	return s.providerTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := lockProviderAccount(ctx, tx, a, false); err != nil {
			return err
		}
		now, err := providerClock(ctx, tx)
		if err != nil {
			return err
		}
		state, _, err := lockProviderPermit(ctx, tx, a, now)
		if err != nil {
			return err
		}
		if state == "finished" || state == "aborted" {
			return nil
		}
		if state == "reserved" && result.Status != 0 {
			return providercall.ErrInvalid
		}
		if err = finishProviderPermit(ctx, tx, a, state, result, now); err != nil {
			return err
		}
		return applyProviderResponse(ctx, tx, a, result, now)
	})
}

func finishProviderPermit(ctx context.Context, tx pgx.Tx, a providercall.Attempt, prior string, r providercall.Result, now time.Time) error {
	state := "finished"
	if r.Status == 0 {
		state = "aborted"
	}
	_, err := tx.Exec(ctx, `UPDATE provider_call_permits SET state=$2,response_status=$3,finished_at=$4 WHERE id=$1`, a.ID, state, r.Status, now)
	if err != nil {
		return fmt.Errorf("release provider permit: %w", err)
	}
	if prior == "reserved" {
		return nil
	}
	_, err = tx.Exec(ctx, `UPDATE provider_call_accounts SET completed=completed+1,next_send_at=GREATEST(next_send_at,$4::timestamptz+(interval_ns::double precision/1000000000)*interval '1 second'),rate_limited=rate_limited+CASE WHEN $3=429 THEN 1 ELSE 0 END,updated_at=$4 WHERE provider=$1 AND account_id=$2 AND EXISTS(SELECT 1 FROM provider_call_permits p WHERE p.id=$5 AND p.started_at IS NOT NULL)`, a.Scope.Provider, a.Scope.Account, r.Status, now, a.ID)
	if err != nil {
		return fmt.Errorf("complete provider attempt count: %w", err)
	}
	return nil
}

func applyProviderResponse(ctx context.Context, tx pgx.Tx, a providercall.Attempt, r providercall.Result, now time.Time) error {
	switch r.Status {
	case http.StatusUnauthorized:
		_, err := tx.Exec(ctx, `INSERT INTO provider_call_rejected_credentials(provider,account_id,tenant_id,credential_revision,rejected_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, a.Scope.Provider, a.Scope.Account, a.Scope.Tenant, a.Scope.CredentialRevision, now)
		if err != nil {
			return fmt.Errorf("reject provider credential revision: %w", err)
		}
	case http.StatusForbidden:
		_, err := tx.Exec(ctx, `UPDATE provider_call_methods SET cooldown_until=GREATEST(cooldown_until,$4) WHERE provider=$1 AND account_id=$2 AND method_key=$3`, a.Scope.Provider, a.Scope.Account, a.Method, now.Add(r.MethodCooldown))
		if err != nil {
			return fmt.Errorf("record provider method denial: %w", err)
		}
	}
	if r.Cooldown > 0 || !r.CooldownUntil.IsZero() || r.IndefiniteCooldown {
		return applyProviderCooldown(ctx, tx, a, r, now)
	}
	return nil
}

func applyProviderCooldown(ctx context.Context, tx pgx.Tx, a providercall.Attempt, r providercall.Result, now time.Time) error {
	until := now.Add(r.Cooldown)
	if r.CooldownUntil.After(until) {
		until = r.CooldownUntil
	}
	// A forged or unexpectedly long delay fails closed instead of being clamped
	// into a retry that would precede the provider's requested time.
	blocked := r.IndefiniteCooldown || until.After(now.Add(24*time.Hour))
	if blocked {
		until = now.Add(24 * time.Hour)
	}
	_, err := tx.Exec(ctx, `UPDATE provider_call_accounts SET cooldown_until=GREATEST(cooldown_until,$3),cooldown_blocked=cooldown_blocked OR $4,updated_at=$5 WHERE provider=$1 AND account_id=$2`, a.Scope.Provider, a.Scope.Account, until, blocked, now)
	if err != nil {
		return fmt.Errorf("record provider cooldown: %w", err)
	}
	return nil
}
