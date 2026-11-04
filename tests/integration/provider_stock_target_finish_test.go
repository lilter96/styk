//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/providercall"
)

type stockTargetFinishFixture struct {
	db    *postgres.Store
	ctx   context.Context
	lease providercall.Lease
}

func stockTargetFinish(t *testing.T, started bool) stockTargetFinishFixture {
	t.Helper()
	db := catalogSyncDB(t)
	a := providercall.Attempt{ID: strings.Repeat("a", 32), Scope: providercall.Scope{Tenant: "target-tenant", Provider: "ozon", Account: "target-account"}, Method: stockFinishDigest("POST\x00v2/products/stocks"), Operation: stockFinishDigest("v2/products/stocks"), TargetKey: stockFinishDigest("product:warehouse"), IsWrite: true, Interval: 20 * time.Millisecond, TargetInterval: 120 * time.Second, MaxInflight: 2, LeaseDuration: 5 * time.Second}
	admission, err := db.TryAcquireProviderCall(context.Background(), a)
	if err != nil || admission.Delay != 0 {
		t.Fatal("admit target fixture", err)
	}
	f := stockTargetFinishFixture{db: db, ctx: context.Background(), lease: admission.Lease}
	if started {
		if delay, startErr := db.StartProviderCall(f.ctx, f.lease); startErr != nil || delay != 0 {
			t.Fatal("start target fixture", startErr)
		}
	}
	return f
}

func stockFinishDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (f stockTargetFinishFixture) deadline(t *testing.T) time.Time {
	t.Helper()
	var result time.Time
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT next_send_at FROM provider_call_targets WHERE provider='ozon' AND account_id='target-account'`).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestProviderStockTargetFinishAnchorsCooldownToDBCompletion(t *testing.T) {
	f := stockTargetFinish(t, true)
	initial := f.deadline(t)
	time.Sleep(25 * time.Millisecond)                // Simulates final authorization/network latency after grant.
	f.lease.Attempt.TargetInterval = time.Nanosecond // Caller data is not policy authority.
	restarted, err := postgres.Open(f.ctx, f.db.Pool.Config().ConnString(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Pool.Close()
	if err := restarted.FinishProviderCall(f.ctx, f.lease, providercall.Result{Status: 200}); err != nil {
		t.Fatal(err)
	}
	var finished time.Time
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT finished_at FROM provider_call_permits WHERE id=$1`, f.lease.Attempt.ID).Scan(&finished); err != nil {
		t.Fatal(err)
	}
	if next := f.deadline(t); next.Before(finished.Add(120*time.Second)) || !next.After(initial) {
		t.Fatal("slow dispatch shortened actual stock target interval")
	}
}

func TestProviderStockTargetFinishNeverShortensFutureDeadlineOrReplays(t *testing.T) {
	f := stockTargetFinish(t, true)
	var expected time.Time
	if err := f.db.Pool.QueryRow(f.ctx, `UPDATE provider_call_targets SET next_send_at=clock_timestamp()+interval '1 hour' RETURNING next_send_at`).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	if err := f.db.FinishProviderCall(f.ctx, f.lease, providercall.Result{}); err != nil {
		t.Fatal(err)
	}
	if !f.deadline(t).Equal(expected) {
		t.Fatal("unknown-response completion shortened later target cooldown")
	}
	if err := f.db.FinishProviderCall(f.ctx, f.lease, providercall.Result{Status: 200}); err != nil {
		t.Fatal(err)
	}
	var completed int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT completed FROM provider_call_accounts WHERE account_id='target-account'`).Scan(&completed); err != nil || completed != 1 {
		t.Fatal("duplicate completion changed attempt count", err)
	}
	if !f.deadline(t).Equal(expected) {
		t.Fatal("late duplicate completion changed target deadline")
	}
}

func TestProviderStockTargetFinishRejectsSubstitution(t *testing.T) {
	f := stockTargetFinish(t, true)
	expected := f.deadline(t)
	for _, mutate := range []func(*providercall.Attempt){
		func(a *providercall.Attempt) { a.TargetKey = stockFinishDigest("foreign:warehouse") },
		func(a *providercall.Attempt) { a.Scope.Tenant = "foreign" }, func(a *providercall.Attempt) { a.Scope.CredentialRevision = 1 },
		func(a *providercall.Attempt) { a.Method = stockFinishDigest("GET\x00v2/products/stocks") },
		func(a *providercall.Attempt) { a.Operation = stockFinishDigest("v1/posting/fbs/split") },
	} {
		forged := f.lease
		mutate(&forged.Attempt)
		if err := f.db.FinishProviderCall(f.ctx, forged, providercall.Result{Status: 200}); !errors.Is(err, providercall.ErrLeaseLost) {
			t.Fatal("substituted finish was accepted", err)
		}
		if !f.deadline(t).Equal(expected) {
			t.Fatal("substituted finish changed target cooldown")
		}
	}
}

func TestProviderStockTargetUnsentAbortDoesNotInventCooldown(t *testing.T) {
	f := stockTargetFinish(t, false)
	if err := f.db.FinishProviderCall(f.ctx, f.lease, providercall.Result{}); err != nil {
		t.Fatal(err)
	}
	var finite bool
	var attempts, completed int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT isfinite(t.next_send_at),a.attempts,a.completed FROM provider_call_targets t JOIN provider_call_accounts a USING(provider,account_id) WHERE a.account_id='target-account'`).Scan(&finite, &attempts, &completed); err != nil {
		t.Fatal(err)
	}
	if finite || attempts != 0 || completed != 0 {
		t.Fatal("unsent abort invented a stock network attempt or cooldown")
	}
}

func TestProviderExpiredStockGrantCannotChangeAnotherTarget(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(strconv.FormatBool(started), func(t *testing.T) {
			f := stockTargetFinish(t, started)
			if _, err := f.db.Pool.Exec(f.ctx, `UPDATE provider_call_permits SET created_at=clock_timestamp()-interval '2 seconds',lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, f.lease.Attempt.ID); err != nil {
				t.Fatal(err)
			}
			time.Sleep(25 * time.Millisecond)
			next := f.lease.Attempt
			next.ID, next.TargetKey = strings.Repeat("b", 32), stockFinishDigest("foreign:warehouse")
			if admission, err := f.db.TryAcquireProviderCall(f.ctx, next); err != nil || admission.Delay != 0 {
				t.Fatal("normal expiry/admission failed", err)
			}
			if err := f.db.FinishProviderCall(f.ctx, f.lease, providercall.Result{}); err != nil {
				t.Fatal(err)
			}
			var finite bool
			if err := f.db.Pool.QueryRow(f.ctx, `SELECT isfinite(next_send_at) FROM provider_call_targets WHERE target_key=$1`, next.TargetKey).Scan(&finite); err != nil {
				t.Fatal(err)
			}
			if finite {
				t.Fatal("late expired callback changed unrelated target cooldown")
			}
			if !started {
				if err := f.db.Pool.QueryRow(f.ctx, `SELECT isfinite(next_send_at) FROM provider_call_targets WHERE target_key=$1`, f.lease.Attempt.TargetKey).Scan(&finite); err != nil || finite {
					t.Fatal("expired unsent grant invented stock cooldown", err)
				}
			}
		})
	}
}
