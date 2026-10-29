//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/testpublication"
)

func TestExpiredMockClaimCannotFinishBeforeReclaim(t *testing.T) {
	db, _ := controlledFixture(t)
	ctx := context.Background()
	tests := []struct {
		name       string
		processErr error
	}{
		{name: "success"},
		{name: "failure", processErr: errors.New("synthetic failure")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertExpiredMockClaim(t, ctx, db, "expired-mock-"+tc.name, tc.processErr)
		})
	}
}

func assertExpiredMockClaim(t *testing.T, ctx context.Context, db *postgres.Store, key string, processErr error) {
	t.Helper()
	_, err := db.Submit(ctx, domain.Observation{OperationKey: key, Source: "test", SKU: "test", Quantity: 1}, 3)
	if err != nil {
		t.Fatal(err)
	}
	old, err := db.Claim(ctx, time.Minute)
	if err != nil || old == nil || old.OperationKey != key {
		t.Fatalf("claim: %+v %v", old, err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE outbox_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.Finish(ctx, *old, "stale-result", processErr, 0); err == nil {
		t.Fatal("expired claim was allowed to finish before reclaim")
	}
	var state string
	var result, lastError *string
	if err = db.Pool.QueryRow(ctx, `SELECT state,result,last_error FROM outbox_tasks WHERE id=$1`, old.ID).Scan(&state, &result, &lastError); err != nil || state != "running" || result != nil || lastError != nil {
		t.Fatalf("expired claim changed the task: state=%s err=%v", state, err)
	}
	fresh, err := db.Claim(ctx, time.Minute)
	if err != nil || fresh == nil || fresh.ID != old.ID || fresh.ClaimToken == old.ClaimToken {
		t.Fatalf("reclaim failed: %+v %v", fresh, err)
	}
	if err = db.Finish(ctx, *old, "stale-result", nil, 0); err == nil {
		t.Fatal("replaced claim was allowed to finish")
	}
	if err = db.Finish(ctx, *fresh, "fresh-result", nil, 0); err != nil {
		t.Fatalf("fresh claim could not finish: %v", err)
	}
}

func TestMockFinishRechecksLeaseAfterRowLockWait(t *testing.T) {
	db, _ := controlledFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := db.Submit(ctx, domain.Observation{OperationKey: "mock-lock-wait", Source: "test", SKU: "test", Quantity: 1}, 3)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := db.Claim(ctx, time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	err = runAfterLeaseLockWait(t, ctx, db, *claim, "", func() error {
		return db.Finish(ctx, *claim, "stale-result", nil, 0)
	})
	if err == nil {
		t.Fatal("finish completed after the lease expired while waiting for the row lock")
	}
}

func TestImportClaimExpiresWhileWaitingForRowLock(t *testing.T) {
	tests := []struct {
		name          string
		lockOperation bool
		mutate        func(context.Context, *postgres.Store, domain.Task) error
	}{
		{"renew waits for task", false, func(ctx context.Context, db *postgres.Store, task domain.Task) error {
			return db.RenewImportClaim(ctx, task, time.Minute)
		}},
		{"progress waits for task", false, func(ctx context.Context, db *postgres.Store, task domain.Task) error {
			return db.ImportProgress(ctx, task, "READING_STOCK", domain.ImportCounts{})
		}},
		{"progress waits for operation", true, func(ctx context.Context, db *postgres.Store, task domain.Task) error {
			return db.ImportProgress(ctx, task, "READING_STOCK", domain.ImportCounts{})
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { testImportClaimLockExpiry(t, tc.lockOperation, tc.mutate) })
	}
}

func testImportClaimLockExpiry(t *testing.T, lockOperation bool, mutate func(context.Context, *postgres.Store, domain.Task) error) {
	t.Helper()
	db, _ := controlledFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	scope := testpublication.Scope()
	op, _, err := db.SubmitCatalogImport(ctx, scope.Tenant, "ozon", scope.OzonAccount, "expired-import", "synthetic", domain.DefaultOzonImportLimits())
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.Claim(ctx, time.Minute)
	if err != nil || task == nil || task.ImportOperationID != op.ID {
		t.Fatalf("claim: %+v %v", task, err)
	}
	operationID := ""
	if lockOperation {
		operationID = op.ID
	}
	err = runAfterLeaseLockWait(t, ctx, db, *task, operationID, func() error { return mutate(ctx, db, *task) })
	if !errors.Is(err, postgres.ErrImportClaimLost) {
		t.Fatalf("expired import claim remained valid after waiting: %v", err)
	}
	var expired bool
	var phase string
	if err = db.Pool.QueryRow(ctx, `SELECT t.lease_until<=clock_timestamp(),o.phase FROM outbox_tasks t JOIN ozon_import_operations o ON o.id=t.import_operation_id WHERE t.id=$1`, task.ID).Scan(&expired, &phase); err != nil || !expired || phase != "READING_PRODUCTS" {
		t.Fatalf("expired claim changed persisted state: expired=%v phase=%s err=%v", expired, phase, err)
	}
}

func runAfterLeaseLockWait(t *testing.T, ctx context.Context, db *postgres.Store, task domain.Task, operationID string, action func() error) error {
	t.Helper()
	if _, err := db.Pool.Exec(ctx, `UPDATE outbox_tasks SET lease_until=clock_timestamp()+interval '3 seconds' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	query, id := `SELECT pg_backend_pid() FROM outbox_tasks WHERE id=$1 FOR UPDATE`, any(task.ID)
	if operationID != "" {
		query, id = `SELECT pg_backend_pid() FROM ozon_import_operations WHERE id=$1 FOR UPDATE`, operationID
	}
	var blocker int
	if err = tx.QueryRow(ctx, query, id).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- action() }()
	waitForBlockedOperation(t, ctx, db, blocker)
	waitForLeaseExpiry(t, ctx, tx, task.ID)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return <-done
}

func rollbackLeaseTest(t *testing.T, tx pgx.Tx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Errorf("rollback lock fixture: %v", err)
	}
}

func waitForBlockedOperation(t *testing.T, ctx context.Context, db *postgres.Store, blocker int) {
	t.Helper()
	for {
		var blocked bool
		if err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("operation did not wait for the lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForLeaseExpiry(t *testing.T, ctx context.Context, tx pgx.Tx, id int64) {
	t.Helper()
	for {
		var expired bool
		if err := tx.QueryRow(ctx, `SELECT lease_until<=clock_timestamp() FROM outbox_tasks WHERE id=$1`, id).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("lease did not expire")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
