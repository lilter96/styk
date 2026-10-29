//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
)

type dispatchCheck struct {
	allowed bool
	err     error
}

func TestControlledDispatchRechecksExpiredLeaseAfterReviewLock(t *testing.T) {
	db, op := controlledClaim(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var taskID int64
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM outbox_tasks WHERE publication_operation_id=$1`, op.ID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE outbox_tasks SET lease_until=clock_timestamp()+interval '3 seconds' WHERE id=$1`, taskID); err != nil {
		t.Fatal(err)
	}
	tx, blocker := lockReviewSource(t, ctx, db, "ozon:"+op.OzonAccount)
	defer rollbackLeaseTest(t, tx)
	done := startDispatchCheck(ctx, db, op)
	waitForBlockedOperation(t, ctx, db, blocker)
	waitForLeaseExpiry(t, ctx, tx, taskID)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || result.allowed {
		t.Fatalf("expired claim permitted dispatch after review lock: allowed=%v err=%v", result.allowed, result.err)
	}
}

func TestControlledDispatchRechecksLeaseAfterReviewRowWait(t *testing.T) {
	db, op := controlledClaim(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var taskID int64
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM outbox_tasks WHERE publication_operation_id=$1`, op.ID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE outbox_tasks SET lease_until=clock_timestamp()+interval '3 seconds' WHERE id=$1`, taskID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	var blocker int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM stock_connections WHERE tenant_id=$1 AND source='moysklad' AND account_id=$2 FOR UPDATE`, op.Tenant, op.MoyskladAccount).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	done := startDispatchCheck(ctx, db, op)
	waitForBlockedOperation(t, ctx, db, blocker)
	waitForLeaseExpiry(t, ctx, tx, taskID)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if result := <-done; result.err != nil || result.allowed {
		t.Fatalf("expired claim permitted dispatch after review row wait: allowed=%v err=%v", result.allowed, result.err)
	}
}

func TestControlledDispatchSeesImportCommittedDuringReviewLock(t *testing.T) {
	db, op := controlledClaim(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, blocker := lockReviewSource(t, ctx, db, "moysklad:"+op.MoyskladAccount)
	defer rollbackLeaseTest(t, tx)
	_, err := tx.Exec(ctx, `INSERT INTO source_imports(source,account_id,fingerprint,started_at,finished_at,report_scope) VALUES('moysklad',$1,'synthetic-new-empty-import',clock_timestamp(),clock_timestamp(),'synthetic')`, op.MoyskladAccount)
	if err != nil {
		t.Fatal(err)
	}
	done := startDispatchCheck(ctx, db, op)
	waitForBlockedOperation(t, ctx, db, blocker)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || result.allowed {
		t.Fatalf("changed source evidence permitted dispatch: allowed=%v err=%v", result.allowed, result.err)
	}
}

func TestControlledDispatchSeesCredentialCommittedDuringReviewLock(t *testing.T) {
	db, op := controlledClaim(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	key := credentials.ScopeKey(credentials.Scope{Provider: "moysklad", Account: op.MoyskladAccount})
	tx, blocker := lockReviewSource(t, ctx, db, key)
	defer rollbackLeaseTest(t, tx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM stock_connections WHERE tenant_id=$1 AND source='moysklad' AND account_id=$2 FOR UPDATE`, op.Tenant, op.MoyskladAccount); err != nil {
		t.Fatal(err)
	}
	_, err := tx.Exec(ctx, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,enabled,ciphertext,checked_at,last_request_id) VALUES($1,'moysklad',$2,'synthetic',1,true,decode(repeat('00',32),'hex'),clock_timestamp(),'synthetic')`, op.Tenant, op.MoyskladAccount)
	if err != nil {
		t.Fatal(err)
	}
	done := startDispatchCheck(ctx, db, op)
	waitForBlockedOperation(t, ctx, db, blocker)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || result.allowed {
		t.Fatalf("changed credential permitted dispatch: allowed=%v err=%v", result.allowed, result.err)
	}
}

func TestControlledDispatchRechecksProofExpiryAfterReviewRowWait(t *testing.T) {
	db, base := controlledFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	until := time.Now().UTC().Add(4 * time.Second)
	for i := range base.Attestations[0].Proofs {
		base.Attestations[0].Proofs[i].ValidUntil = until
	}
	review, err := db.PreviewStockSync(ctx, domain.StockSyncPreviewRequest{StockSyncScope: base.StockSyncScope, Attestations: base.Attestations}, time.Now().UTC())
	if err != nil || !review.Ready {
		t.Fatalf("short-lived review unavailable: %v", err)
	}
	controlledEnable(t, db, review)
	op, err := db.NextControlledStockPublication(ctx, time.Now().UTC())
	if err != nil || op == nil {
		t.Fatalf("claim controlled publication: %v", err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	var blocker int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM stock_connections WHERE tenant_id=$1 AND source='moysklad' AND account_id=$2 FOR UPDATE`, op.Tenant, op.MoyskladAccount).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	done := startDispatchCheck(ctx, db, *op)
	waitForBlockedOperation(t, ctx, db, blocker)
	waitForDatabaseTime(t, ctx, tx, until)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if result := <-done; result.err != nil || result.allowed {
		t.Fatalf("expired proof permitted dispatch after review row wait: allowed=%v err=%v", result.allowed, result.err)
	}
}

func waitForDatabaseTime(t *testing.T, ctx context.Context, tx pgx.Tx, until time.Time) {
	t.Helper()
	for {
		var expired bool
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()>=$1`, until).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			return
		}
		if ctx.Err() != nil {
			t.Fatal("proof did not expire")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestControlledDispatchReleasesLocksWhenReviewWaitIsCanceled(t *testing.T) {
	db, op := controlledClaim(t)
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	tx, blocker := lockReviewSource(t, ctx, db, "ozon:"+op.OzonAccount)
	defer rollbackLeaseTest(t, tx)
	attempt, cancel := context.WithCancel(ctx)
	done := startDispatchCheck(attempt, db, op)
	waitForBlockedOperation(t, ctx, db, blocker)
	cancel()
	if result := <-done; result.err == nil || result.allowed {
		t.Fatalf("canceled review succeeded: allowed=%v err=%v", result.allowed, result.err)
	}
	key := "stock-publication:" + op.Tenant + ":" + op.MoyskladAccount + ":" + op.OzonAccount
	assertAdvisoryLockReleased(t, ctx, db, key)
}

func TestControlledDispatchReleasesLocksWhenReviewRowWaitIsCanceled(t *testing.T) {
	db, op := controlledClaim(t)
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	var blocker int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM stock_connections WHERE tenant_id=$1 AND source='moysklad' AND account_id=$2 FOR UPDATE`, op.Tenant, op.MoyskladAccount).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	attempt, cancel := context.WithCancel(ctx)
	done := startDispatchCheck(attempt, db, op)
	waitForBlockedOperation(t, ctx, db, blocker)
	cancel()
	if result := <-done; result.err == nil || result.allowed {
		t.Fatalf("canceled review succeeded: allowed=%v err=%v", result.allowed, result.err)
	}
	key := "stock-publication:" + op.Tenant + ":" + op.MoyskladAccount + ":" + op.OzonAccount
	assertAdvisoryLockReleased(t, ctx, db, key)
}

func assertAdvisoryLockReleased(t *testing.T, ctx context.Context, db *postgres.Store, key string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, db.Pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Errorf("close verification connection: %v", err)
		}
	}()
	for {
		var acquired bool
		if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, key).Scan(&acquired); err != nil {
			t.Fatal(err)
		}
		if acquired {
			var unlocked bool
			if err = conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key).Scan(&unlocked); err != nil || !unlocked {
				t.Fatalf("release verification lock: %v", err)
			}
			return
		}
		if ctx.Err() != nil {
			t.Fatal("canceled dispatch left its advisory lock held")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func controlledClaim(t *testing.T) (*postgres.Store, domain.StockPublication) {
	t.Helper()
	db, review := controlledFixture(t)
	controlledEnable(t, db, review)
	op, err := db.NextControlledStockPublication(context.Background(), time.Now().UTC())
	if err != nil || op == nil {
		t.Fatalf("claim controlled publication: %v", err)
	}
	return db, *op
}

func lockReviewSource(t *testing.T, ctx context.Context, db *postgres.Store, key string) (pgx.Tx, int) {
	t.Helper()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		rollbackLeaseTest(t, tx)
		t.Fatal(err)
	}
	var blocker int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
		rollbackLeaseTest(t, tx)
		t.Fatal(err)
	}
	return tx, blocker
}

func startDispatchCheck(ctx context.Context, db *postgres.Store, op domain.StockPublication) <-chan dispatchCheck {
	done := make(chan dispatchCheck, 1)
	go func() {
		allowed, err := db.StockPublicationCanDispatch(ctx, op, time.Now().UTC())
		done <- dispatchCheck{allowed, err}
	}()
	return done
}
