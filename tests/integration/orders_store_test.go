//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersStoreConfigRevisionAndActor(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	request := orders.SaveConfigRequest{Config: fixture.config, ExpectedRevision: fixture.config.Revision, Actor: fixture.session.AuditID, IdempotencyKey: "config-next"}
	request.Config.StateID = "native-state-next"
	var wait sync.WaitGroup
	results := make(chan orders.Config, 2)
	errorsOut := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			config, err := fixture.db.SaveOrderConfig(fixture.ctx, request, time.Now())
			results <- config
			errorsOut <- err
		}()
	}
	wait.Wait()
	for range 2 {
		config, err := <-results, <-errorsOut
		if err != nil || config.Revision != 2 {
			t.Fatalf("concurrent request replay: revision=%d error=%v", config.Revision, err)
		}
	}
	request.Config.AgentID = "different-agent"
	if _, err := fixture.db.SaveOrderConfig(fixture.ctx, request, time.Now()); !errors.Is(err, orders.ErrConflict) {
		t.Fatalf("changed idempotency payload accepted: %v", err)
	}
	request.IdempotencyKey = "forged-actor"
	if _, err := fixture.db.SaveOrderConfig(context.Background(), request, time.Now()); !errors.Is(err, orders.ErrBlocked) {
		t.Fatalf("actor string without authenticated context accepted: %v", err)
	}
}

func TestOrdersStoreClaimUnknownAndExactIntent(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	posting := ordersStorePosting(fixture.config.Scope, "posting-1", "source-1", time.Now().UTC().Truncate(time.Microsecond))
	ordersStoreCommit(t, fixture, posting, "ingest-first")
	operation := ordersStoreApprove(t, fixture, ordersStorePreview(t, fixture, posting.PostingNumber))
	if task, err := fixture.db.Claim(fixture.ctx, time.Minute); err != nil || task != nil {
		t.Fatalf("generic worker claimed order write: %+v %v", task, err)
	}
	work := ordersConcurrentClaim(t, fixture)
	allowed, err := fixture.db.GuardOrderDispatch(fixture.ctx, orders.DispatchGuard{Work: work, Begin: true})
	if err != nil || !allowed {
		t.Fatalf("begin exact dispatch: allowed=%v error=%v", allowed, err)
	}
	if again, guardErr := fixture.db.GuardOrderDispatch(fixture.ctx, orders.DispatchGuard{Work: work, Begin: true}); guardErr != nil || again {
		t.Fatalf("write intent dispatched twice: allowed=%v error=%v", again, guardErr)
	}
	completion := orders.Completion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: orders.StateDispatching, State: orders.StateUnknown, Code: "NETWORK_UNKNOWN", Receipt: orders.Receipt{CommandID: work.Command.ID, State: orders.StateUnknown, Code: "NETWORK_UNKNOWN", ObservedAt: time.Now()}}
	if err = fixture.db.CompleteOrderWork(fixture.ctx, completion); err != nil {
		t.Fatal(err)
	}
	readback := ordersStoreClaim(t, fixture, time.Minute)
	if readback.State != orders.StateUnknown || readback.DispatchCount != 1 {
		t.Fatalf("unknown outcome lost dispatch fence: state=%s count=%d", readback.State, readback.DispatchCount)
	}
	if allowed, err = fixture.db.GuardOrderDispatch(fixture.ctx, orders.DispatchGuard{Work: readback, Begin: true}); err != nil || allowed {
		t.Fatalf("unknown outcome reentered write dispatch: %v %v", allowed, err)
	}
	if err = fixture.db.CompleteOrderWork(fixture.ctx, completion); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("old claim completed new readback: %v", err)
	}
	assertOrdersExactIntent(t, fixture, work)
	history, err := fixture.db.OrderHistory(fixture.ctx, work.Scope, orders.PageRequest{Limit: 10})
	if err != nil || len(history.Operations) != 1 || history.Operations[0].ID != operation.ID || history.Operations[0].State != orders.StateUnknown {
		t.Fatalf("partial operation was represented as complete: %+v %v", history, err)
	}
}

func ordersConcurrentClaim(t *testing.T, fixture ordersStoreFixture) orders.Work {
	t.Helper()
	var wait sync.WaitGroup
	works := make(chan *orders.Work, 2)
	errorsOut := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			now := time.Now()
			work, err := fixture.db.ClaimOrderWork(fixture.ctx, orders.ClaimRequest{WorkerID: "parallel-worker", Now: now, LeaseUntil: now.Add(time.Minute)})
			works <- work
			errorsOut <- err
		}()
	}
	wait.Wait()
	var claimed *orders.Work
	for range 2 {
		work, err := <-works, <-errorsOut
		if err != nil {
			t.Fatal(err)
		}
		if work != nil {
			if claimed != nil {
				t.Fatal("two workers claimed one order task")
			}
			claimed = work
		}
	}
	if claimed == nil {
		t.Fatal("no order work claimed")
	}
	return *claimed
}

func assertOrdersExactIntent(t *testing.T, fixture ordersStoreFixture, work orders.Work) {
	t.Helper()
	var hash string
	var intents, attempts int
	err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT (SELECT fingerprint FROM orders_write_intents WHERE work_id=$1),(SELECT count(*) FROM orders_write_intents WHERE work_id=$1),(SELECT count(*) FROM orders_attempts WHERE work_id=$1 AND phase='DISPATCHING')`, work.ID).Scan(&hash, &intents, &attempts)
	if err != nil || hash != work.CommandFingerprint || intents != 1 || attempts != 1 {
		t.Fatalf("immutable dispatch journal mismatch: intents=%d attempts=%d error=%v", intents, attempts, err)
	}
	if _, err = fixture.db.Pool.Exec(fixture.ctx, `UPDATE orders_write_intents SET fingerprint=repeat('0',64) WHERE work_id=$1`, work.ID); err == nil {
		t.Fatal("exact dispatch intent was mutable")
	}
}

func TestOrdersStoreExpiredDispatchRecoversAsReadOnly(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	posting := ordersStorePosting(fixture.config.Scope, "expired-posting", "source-1", time.Now().UTC().Truncate(time.Microsecond))
	ordersStoreCommit(t, fixture, posting, "expired-ingest")
	ordersStoreApprove(t, fixture, ordersStorePreview(t, fixture, posting.PostingNumber))
	work := ordersStoreClaim(t, fixture, 150*time.Millisecond)
	if allowed, err := fixture.db.GuardOrderDispatch(fixture.ctx, orders.DispatchGuard{Work: work, Begin: true}); err != nil || !allowed {
		t.Fatalf("begin dispatch: %v %v", allowed, err)
	}
	time.Sleep(time.Until(work.LeaseUntil) + 10*time.Millisecond)
	readback := ordersStoreClaim(t, fixture, time.Minute)
	if readback.State != orders.StateUnknown || readback.DispatchCount != 1 || readback.Code != "DISPATCH_LEASE_EXPIRED" {
		t.Fatalf("expired send was made retryable: %+v", readback)
	}
	if allowed, err := fixture.db.GuardOrderDispatch(fixture.ctx, orders.DispatchGuard{Work: readback, Begin: true}); err != nil || allowed {
		t.Fatalf("expired send was resent: %v %v", allowed, err)
	}
}

func TestOrdersStoreDispatchChecksExpiryAfterRowLock(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	posting := ordersStorePosting(fixture.config.Scope, "lock-posting", "source-1", time.Now().UTC().Truncate(time.Microsecond))
	ordersStoreCommit(t, fixture, posting, "lock-ingest")
	ordersStoreApprove(t, fixture, ordersStorePreview(t, fixture, posting.PostingNumber))
	work := ordersStoreClaim(t, fixture, 500*time.Millisecond)
	ctx, cancel := context.WithTimeout(fixture.ctx, 5*time.Second)
	defer cancel()
	tx, err := fixture.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	var pid int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM orders_work WHERE id=$1 FOR UPDATE`, work.ID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		allowed, guardErr := fixture.db.GuardOrderDispatch(ctx, orders.DispatchGuard{Work: work, Begin: true})
		if allowed {
			guardErr = errors.New("expired dispatch allowed")
		}
		done <- guardErr
	}()
	waitForBlockedOperation(t, ctx, fixture.db, pid)
	time.Sleep(time.Until(work.LeaseUntil) + 10*time.Millisecond)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, orders.ErrStale) {
		t.Fatalf("lease expiry was checked before waiting for row lock: %v", err)
	}
}

func TestOrdersStoreProtectsPIIAndCredentialRevision(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	posting := ordersStorePosting(fixture.config.Scope, "private-posting", "source-1", time.Now().UTC().Truncate(time.Microsecond))
	posting.PII = &orders.Customer{Name: orders.Present("PRIVATE_CUSTOMER_SENTINEL")}
	ordersStoreCommit(t, fixture, posting, "private-ingest")
	preview := ordersStorePreview(t, fixture, posting.PostingNumber)
	ordersStoreApprove(t, fixture, preview)
	var evidence string
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT (SELECT string_agg(posting::text,'') FROM orders_posting_observations)||(SELECT string_agg(preview::text,'') FROM orders_previews)||(SELECT string_agg(work::text,'') FROM orders_work)`).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(evidence, "PRIVATE_CUSTOMER_SENTINEL") {
		t.Fatal("customer PII entered normal evidence")
	}
	work := ordersStoreClaim(t, fixture, time.Minute)
	if _, err := fixture.db.Pool.Exec(fixture.ctx, `UPDATE connection_credentials SET revision=revision+1 WHERE tenant_id=$1 AND provider='ozon' AND account_id=$2`, work.Scope.Tenant, work.Scope.OzonAccount); err != nil {
		t.Fatal(err)
	}
	if allowed, err := fixture.db.GuardOrderDispatch(fixture.ctx, orders.DispatchGuard{Work: work, Begin: true}); err != nil || allowed {
		t.Fatalf("stale credential command dispatched: %v %v", allowed, err)
	}
}

func TestOrdersStoreReconciliationRequiresEveryNativePosition(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	posting := ordersStorePosting(fixture.config.Scope, "readback-posting", "source-1", time.Now().UTC().Truncate(time.Microsecond))
	ordersStoreCommit(t, fixture, posting, "readback-ingest")
	ordersStoreApprove(t, fixture, ordersStorePreview(t, fixture, posting.PostingNumber))
	work := ordersStoreClaim(t, fixture, time.Minute)
	correlation := work.Command.Correlation
	correlation.CustomerOrderID = "native-customer-order"
	target := orders.TargetObservation{Scope: work.Scope, Correlation: correlation, Revision: "native-r1", Fields: *work.Command.CustomerOrder, Complete: false, ObservedAt: time.Now()}
	completion := orders.Completion{WorkID: work.ID, Scope: work.Scope, ClaimToken: work.ClaimToken, ExpectedState: orders.StateClaimed, State: orders.StateReconciled, ObservedTarget: &target}
	if err := fixture.db.CompleteOrderWork(fixture.ctx, completion); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("partial embedded positions marked complete: %v", err)
	}
	target.Complete = true
	position := work.Command.CustomerOrder.Positions[0]
	target.Positions = []orders.TargetPosition{{ID: "native-position", Identity: position.Identity, MoyskladKind: position.Mapping.MoyskladKind, MoyskladID: position.Mapping.MoyskladID, Quantity: "1", Price: position.Price, VAT: position.VAT}}
	if err := fixture.db.CompleteOrderWork(fixture.ctx, completion); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("wrong native quantity marked complete: %v", err)
	}
	target.Positions[0].Quantity = "2.000"
	if err := fixture.db.CompleteOrderWork(fixture.ctx, completion); err != nil {
		t.Fatal(err)
	}
	history, err := fixture.db.OrderHistory(fixture.ctx, work.Scope, orders.PageRequest{Limit: 10})
	if err != nil || history.Operations[0].State != orders.StateReconciled {
		t.Fatalf("complete exact native readback not reconciled: %+v %v", history, err)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_reconciliations WHERE observed_target IS NOT NULL`, 1)
}
