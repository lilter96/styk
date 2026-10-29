//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/postgres"
)

type orderDispatchLockResult struct {
	operation string
	allowed   bool
	err       error
}

// Dispatch authorization and order evidence must acquire the same account
// credential locks in the same order. No provider requests are made here.
func TestOrderEvidenceAndDispatchUseCompatibleCredentialLocks(t *testing.T) {
	db, review := controlledFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	appName := fmt.Sprintf("order-dispatch-regression-%d", time.Now().UnixNano())
	db = orderDispatchTestStore(t, ctx, db, appName)
	controlledEnable(t, db, review)
	op, err := db.NextControlledStockPublication(ctx, time.Now().UTC())
	if err != nil || op == nil {
		t.Fatalf("claim controlled publication: %v", err)
	}
	blocker, blockerPID := blockOrderDispatchCredential(t, ctx, db, "ozon", op.OzonAccount)
	results := make(chan orderDispatchLockResult, 2)
	received := 0
	defer func() {
		cancel()
		cleanupOrderDispatchLock(t, blocker, results, received)
	}()
	go func() {
		allowed, e := db.StockPublicationCanDispatch(ctx, *op, time.Now().UTC())
		results <- orderDispatchLockResult{operation: "dispatch", allowed: allowed, err: e}
	}()
	go func() {
		_, e := db.StartOrderEvidence(ctx, op.StockSyncScope, 1, time.Now().UTC())
		results <- orderDispatchLockResult{operation: "orders", allowed: true, err: e}
	}()
	waitForOrderDispatchWaiters(t, ctx, db, blockerPID, appName)
	assertMoyCredentialLockFree(t, ctx, db, op.MoyskladAccount)
	if err = blocker.Commit(ctx); err != nil {
		t.Fatalf("release credential lock: %v", err)
	}
	for received < 2 {
		result := <-results
		received++
		if result.err != nil || !result.allowed {
			t.Fatalf("%s did not finish safely: allowed=%v err=%v", result.operation, result.allowed, result.err)
		}
	}
}

func orderDispatchTestStore(t *testing.T, ctx context.Context, db *postgres.Store, appName string) *postgres.Store {
	t.Helper()
	config := db.Pool.Config()
	config.ConnConfig.RuntimeParams["application_name"] = appName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create tagged test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return &postgres.Store{Pool: pool, Timeout: db.Timeout}
}

func blockOrderDispatchCredential(t *testing.T, ctx context.Context, db *postgres.Store, provider, account string) (pgx.Tx, int) {
	t.Helper()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	key := credentials.ScopeKey(credentials.Scope{Provider: provider, Account: account})
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
			t.Errorf("rollback lock holder: %v", rollbackErr)
		}
		t.Fatalf("acquire credential lock: %v", err)
	}
	var pid int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
			t.Errorf("rollback lock holder: %v", rollbackErr)
		}
		t.Fatalf("read lock holder: %v", err)
	}
	return tx, pid
}

func waitForOrderDispatchWaiters(t *testing.T, ctx context.Context, db *postgres.Store, pid int, appName string) {
	t.Helper()
	for {
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE application_name=$2 AND $1=ANY(pg_blocking_pids(pid))`, pid, appName).Scan(&count); err != nil {
			t.Fatalf("observe blocked operations: %v", err)
		}
		if count >= 2 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("dispatch and order operations did not both wait for Ozon credentials")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func assertMoyCredentialLockFree(t *testing.T, ctx context.Context, db *postgres.Store, account string) {
	t.Helper()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin credential lock probe: %v", err)
	}
	defer func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback credential lock probe: %v", err)
		}
	}()
	key := credentials.ScopeKey(credentials.Scope{Provider: "moysklad", Account: account})
	var acquired bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, key).Scan(&acquired); err != nil {
		t.Fatalf("probe MoySklad credential lock: %v", err)
	}
	if !acquired {
		t.Fatal("an operation held MoySklad credentials while waiting for Ozon credentials")
	}
}

func cleanupOrderDispatchLock(t *testing.T, blocker pgx.Tx, results <-chan orderDispatchLockResult, received int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := blocker.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Errorf("rollback credential lock holder: %v", err)
	}
	for received < 2 {
		select {
		case <-results:
			received++
		case <-ctx.Done():
			t.Error("order or dispatch operation did not exit after cancellation")
			return
		}
	}
}
