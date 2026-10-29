//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/application"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/testpublication"
)

func progressScope(f stockFixtureData) domain.StockSyncScope {
	return domain.StockSyncScope{Tenant: f.req.Pool.Tenant, MoyskladAccount: f.req.Pool.MoyskladAccount, OzonAccount: f.req.Target.OzonAccount}
}

func enableOrderProgressCredentials(t *testing.T, f stockFixtureData) {
	t.Helper()
	for _, c := range []struct{ provider, account string }{{"moysklad", f.req.Pool.MoyskladAccount}, {"ozon", f.req.Target.OzonAccount}} {
		_, err := f.store.Pool.Exec(context.Background(), `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,enabled,ciphertext,checked_at,last_request_id) VALUES($1,$2,$3,'synthetic',1,true,$4,now(),'synthetic-request')`, f.req.Pool.Tenant, c.provider, c.account, make([]byte, 29))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func sourceSlice(run postgres.OrderEvidenceRun, order domain.CustomerOrderObservation) domain.CustomerOrderImport {
	w := run.Source.Pending[0]
	return domain.CustomerOrderImport{Tenant: run.Scope.Tenant, Account: run.Scope.MoyskladAccount, Since: w.From, Until: w.To, StartedAt: run.StartedAt, FinishedAt: time.Now().UTC(), Orders: []domain.CustomerOrderObservation{order}}
}

func postingSlice(run postgres.OrderEvidenceRun, posting domain.PostingObservation) domain.PostingImport {
	w := run.Posting.Pending[0]
	return domain.PostingImport{Tenant: run.Scope.Tenant, Account: run.Scope.OzonAccount, Since: w.From, Until: w.To, StartedAt: run.StartedAt, FinishedAt: time.Now().UTC(), Postings: []domain.PostingObservation{posting}}
}

func assertProgressBatchCounts(t *testing.T, f stockFixtureData, orders, postings int) {
	t.Helper()
	var gotOrders, gotPostings int
	err := f.store.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM moy_order_batches WHERE tenant_id=$1), (SELECT count(*) FROM ozon_posting_batches WHERE tenant_id=$1)`, f.req.Pool.Tenant).Scan(&gotOrders, &gotPostings)
	if err != nil || gotOrders != orders || gotPostings != postings {
		t.Fatalf("unexpected completed batches: orders=%d postings=%d err=%v", gotOrders, gotPostings, err)
	}
}

func TestOrderEvidenceCheckpointsSealOnlyCompleteWindows(t *testing.T) {
	f := newStockFixture(t)
	enableOrderProgressCredentials(t, f)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	run, err := f.store.StartOrderEvidence(ctx, progressScope(f), 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Source.Split() {
		t.Fatal("split failed")
	}
	run, err = f.store.CheckpointOrderEvidence(ctx, run, "moysklad", "IN_PROGRESS", "requests")
	if err != nil {
		t.Fatal(err)
	}
	reserve := "2"
	order := customerOrder(f, "10000000-0000-0000-0000-000000000001", &reserve)
	if err = run.Source.Add(sourceSlice(run, order)); err != nil {
		t.Fatal(err)
	}
	run, err = f.store.CheckpointOrderEvidence(ctx, run, "moysklad", "IN_PROGRESS", "")
	if err != nil {
		t.Fatal(err)
	}
	assertProgressBatchCounts(t, f, 0, 0)
	resumed, err := f.store.StartOrderEvidence(ctx, run.Scope, 1, now.Add(time.Second))
	if err != nil || !resumed.From.Equal(run.From) || !resumed.To.Equal(run.To) || len(resumed.Source.Completed) != 1 || len(resumed.Source.Pending) != 1 {
		t.Fatalf("checkpoint did not resume the same window: %+v %v", resumed, err)
	}
	if err = resumed.Source.Add(sourceSlice(resumed, order)); err != nil {
		t.Fatal(err)
	}
	run, err = f.store.CheckpointOrderEvidence(ctx, resumed, "moysklad", "COMPLETED", "")
	if err != nil || run.SourceBatchID == nil || run.SourceStatus != "COMPLETED" {
		t.Fatalf("seal source: %+v %v", run, err)
	}
	assertProgressBatchCounts(t, f, 1, 0)
	var count int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM moy_order_positions p JOIN moy_order_observations o ON o.id=p.observation_id WHERE o.batch_id=$1 AND p.reserve=2`, *run.SourceBatchID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate reservation observation: %d %v", count, err)
	}
	if !run.Posting.Split() {
		t.Fatal("split posting failed")
	}
	run, err = f.store.CheckpointOrderEvidence(ctx, run, "ozon", "IN_PROGRESS", "requests")
	if err != nil {
		t.Fatal(err)
	}
	post := postingFixture("synthetic-posting", "awaiting_packaging", now, domain.PostingLine{OfferID: "offer", SKU: "sku", Quantity: 2})
	if err = run.Posting.Add(postingSlice(run, post)); err != nil {
		t.Fatal(err)
	}
	run, err = f.store.CheckpointOrderEvidence(ctx, run, "ozon", "IN_PROGRESS", "")
	if err != nil {
		t.Fatal(err)
	}
	bad := post
	bad.Status = "cancelled"
	conflicting := run
	if err = conflicting.Posting.Add(postingSlice(conflicting, bad)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CheckpointOrderEvidence(ctx, conflicting, "ozon", "COMPLETED", ""); !errors.Is(err, application.ErrEvidenceConflict) {
		t.Fatalf("posting status conflict was sealed: %v", err)
	}
	assertProgressBatchCounts(t, f, 1, 0)
	if err = run.Posting.Add(postingSlice(run, post)); err != nil {
		t.Fatal(err)
	}
	run, err = f.store.CheckpointOrderEvidence(ctx, run, "ozon", "COMPLETED", "")
	if err != nil || run.PostingBatchID == nil || run.PostingStatus != "COMPLETED" {
		t.Fatalf("seal posting: %+v %v", run, err)
	}
	assertProgressBatchCounts(t, f, 1, 1)
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM ozon_posting_observations WHERE batch_id=$1 AND status='awaiting_packaging'`, *run.PostingBatchID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate posting or wrong status: %d %v", count, err)
	}
	_, err = f.store.Pool.Exec(ctx, `UPDATE order_evidence_progress SET source_status='COMPLETED', source_batch_id=NULL WHERE id=$1`, run.ID)
	if err == nil {
		t.Fatal("database accepted completion without source batch")
	}
}

func TestOrderEvidenceCredentialChangeExpiryAndTenantIsolation(t *testing.T) {
	f, other := newStockFixture(t), newStockFixture(t)
	enableOrderProgressCredentials(t, f)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	run, err := f.store.StartOrderEvidence(ctx, progressScope(f), 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.StartOrderEvidence(ctx, domain.StockSyncScope{Tenant: other.req.Pool.Tenant, MoyskladAccount: run.Scope.MoyskladAccount, OzonAccount: run.Scope.OzonAccount}, 1, now); err == nil {
		t.Fatal("cross-tenant accounts accepted")
	}
	_, err = f.store.Pool.Exec(ctx, `UPDATE connection_credentials SET revision=revision+1 WHERE tenant_id=$1 AND provider='moysklad' AND account_id=$2`, run.Scope.Tenant, run.Scope.MoyskladAccount)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Source.Split() {
		t.Fatal("split failed")
	}
	if _, err = f.store.CheckpointOrderEvidence(ctx, run, "moysklad", "IN_PROGRESS", "requests"); !errors.Is(err, postgres.ErrOrderEvidenceChanged) {
		t.Fatalf("old credential checkpoint accepted: %v", err)
	}
	summary, err := f.store.CurrentOrderEvidence(ctx, run.Scope)
	if err != nil || summary == nil || !summary.CredentialChanged {
		t.Fatalf("credential change not reported: %+v %v", summary, err)
	}
	fresh, err := f.store.StartOrderEvidence(ctx, run.Scope, 1, now.Add(time.Second))
	if err != nil || fresh.Generation <= run.Generation || fresh.MoyskladRevision != 2 || fresh.SourceCompletedWindows != 0 || fresh.SourcePendingWindows != 1 {
		t.Fatalf("credential change not reset: %+v %v", fresh, err)
	}
	_, err = f.store.Pool.Exec(ctx, `UPDATE order_evidence_progress SET started_at=$1 WHERE id=$2`, now.Add(-31*time.Minute), fresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	summary, err = f.store.CurrentOrderEvidence(ctx, fresh.Scope)
	if err != nil || summary == nil || !summary.Expired {
		t.Fatalf("expiry not reported: %+v %v", summary, err)
	}
	fresh, err = f.store.StartOrderEvidence(ctx, fresh.Scope, 1, now.Add(2*time.Second))
	if err != nil || fresh.Generation <= run.Generation+1 {
		t.Fatalf("expired window was not reset: %+v %v", fresh, err)
	}
	assertProgressBatchCounts(t, f, 0, 0)
}

func TestOrderEvidenceConcurrentCheckpointsAreFenced(t *testing.T) {
	f := newStockFixture(t)
	enableOrderProgressCredentials(t, f)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	run, err := f.store.StartOrderEvidence(ctx, progressScope(f), 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Source.Split() {
		t.Fatal("split failed")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.store.CheckpointOrderEvidence(ctx, run, "moysklad", "IN_PROGRESS", "requests")
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	success, fenced := 0, 0
	for err = range errs {
		if err == nil {
			success++
		} else if errors.Is(err, postgres.ErrOrderEvidenceChanged) {
			fenced++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || fenced != 1 {
		t.Fatalf("checkpoint fencing: success=%d fenced=%d", success, fenced)
	}
	assertProgressBatchCounts(t, f, 0, 0)
}

func TestOrderEvidenceResultGuardRejectsUnrelatedBatches(t *testing.T) {
	f := newStockFixture(t)
	enableOrderProgressCredentials(t, f)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	priorSource := customerBatch(f)
	priorSource.Since, priorSource.Until = now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	priorSource.StartedAt, priorSource.FinishedAt = now.Add(-time.Hour), now.Add(-time.Hour)
	sourceID, err := f.store.SaveCustomerOrders(ctx, priorSource)
	if err != nil {
		t.Fatal(err)
	}
	priorPosting := postingBatch(f.req.Pool.Tenant, f.req.Target.OzonAccount, now.Add(-time.Hour))
	postingID, err := f.store.SavePostings(ctx, priorPosting)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.store.StartOrderEvidence(ctx, progressScope(f), 1, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Pool.Exec(ctx, `UPDATE order_evidence_progress SET source_status='COMPLETED',source_batch_id=$1 WHERE id=$2`, sourceID, run.ID)
	if err == nil {
		t.Fatal("source batch from another window was accepted")
	}
	_, err = f.store.Pool.Exec(ctx, `UPDATE order_evidence_progress SET posting_status='COMPLETED',posting_batch_id=$1 WHERE id=$2`, postingID, run.ID)
	if err == nil {
		t.Fatal("posting batch from another window was accepted")
	}
	var sourceStatus, postingStatus string
	err = f.store.Pool.QueryRow(ctx, `SELECT source_status,posting_status FROM order_evidence_progress WHERE id=$1`, run.ID).Scan(&sourceStatus, &postingStatus)
	if err != nil || sourceStatus != "IN_PROGRESS" || postingStatus != "IN_PROGRESS" {
		t.Fatalf("guard changed status: %s %s %v", sourceStatus, postingStatus, err)
	}
}

func TestOrderEvidenceCheckpointExpiresWhileWaitingForRow(t *testing.T) {
	f := newStockFixture(t)
	enableOrderProgressCredentials(t, f)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	run, err := f.store.StartOrderEvidence(ctx, progressScope(f), 1, now)
	if err != nil {
		t.Fatal(err)
	}
	run.StartedAt = now.Add(-30*time.Minute + 600*time.Millisecond)
	_, err = f.store.Pool.Exec(ctx, `UPDATE order_evidence_progress SET started_at=$1 WHERE id=$2`, run.StartedAt, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Source.Split() {
		t.Fatal("split failed")
	}
	tx, err := f.store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback lock transaction: %v", err)
		}
	}()
	if _, err = tx.Exec(ctx, `SELECT id FROM order_evidence_progress WHERE id=$1 FOR UPDATE`, run.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, e := f.store.CheckpointOrderEvidence(ctx, run, "moysklad", "IN_PROGRESS", "requests")
		result <- e
	}()
	wait := time.Until(run.StartedAt.Add(30*time.Minute)) + 100*time.Millisecond
	if wait > 0 {
		time.Sleep(wait)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, postgres.ErrOrderEvidenceExpired) {
		t.Fatalf("expired waiting checkpoint accepted: %v", err)
	}
	assertProgressBatchCounts(t, f, 0, 0)
}

func TestOrderEvidenceRejectsStaleReaderBeforeCreatingProgress(t *testing.T) {
	f := newStockFixture(t)
	enableOrderProgressCredentials(t, f)
	ctx := context.Background()
	_, err := f.store.StartOrderEvidenceForRevisions(ctx, progressScope(f), 1, time.Now().UTC(), 0, 1)
	if !errors.Is(err, postgres.ErrOrderEvidenceChanged) {
		t.Fatalf("stale credential revision accepted: %v", err)
	}
	var count int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM order_evidence_progress WHERE tenant_id=$1`, f.req.Pool.Tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale reader created progress: %d %v", count, err)
	}
}

func TestOrderEvidenceLegacyWindowNeverResumes(t *testing.T) {
	f := newStockFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	run, err := f.store.StartOrderEvidence(ctx, progressScope(f), 1, now)
	if err != nil || run.ContinuationSupported() {
		t.Fatalf("legacy scope marked resumable: %+v %v", run, err)
	}
	if !run.Source.Split() {
		t.Fatal("split failed")
	}
	run, err = f.store.CheckpointOrderEvidence(ctx, run, "moysklad", "IN_PROGRESS", "requests")
	if err != nil {
		t.Fatal(err)
	}
	summary, err := f.store.CurrentOrderEvidence(ctx, run.Scope)
	if err != nil || summary == nil || summary.ContinuationSupported {
		t.Fatalf("legacy continuation status: %+v %v", summary, err)
	}
	restarted, err := f.store.StartOrderEvidence(ctx, run.Scope, 1, now.Add(time.Second))
	if err != nil || restarted.Generation <= run.Generation || restarted.SourcePendingWindows != 1 || restarted.SourceCompletedWindows != 0 || restarted.From.Equal(run.From) {
		t.Fatalf("legacy progress resumed: %+v %v", restarted, err)
	}
	assertProgressBatchCounts(t, f, 0, 0)
}

func TestOrderEvidenceAndProgressShareSnapshotDuringSeal(t *testing.T) {
	db, _ := controlledFixture(t)
	ctx := context.Background()
	scope := testpublication.Scope()
	run, err := db.StartOrderEvidence(ctx, scope, 1, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	w := run.Source.Pending[0]
	batch := domain.CustomerOrderImport{Tenant: scope.Tenant, Account: scope.MoyskladAccount, Since: w.From, Until: w.To, StartedAt: run.StartedAt, FinishedAt: time.Now().UTC()}
	if err = run.Source.Add(batch); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, e := db.CheckpointOrderEvidence(ctx, run, "moysklad", "COMPLETED", ""); result <- e }()
	for {
		e, p, eErr := db.StockOrderEvidenceWithProgress(ctx, scope, testpublication.OzonProduct, testpublication.OzonWarehouse)
		if eErr != nil || p == nil {
			t.Fatalf("read evidence snapshot: %+v %v", p, eErr)
		}
		if p.SourceStatus == "COMPLETED" {
			if p.SourceBatchID == nil || e.BatchID == nil || *e.BatchID != *p.SourceBatchID {
				t.Fatalf("completed progress combined with stale evidence: %+v %+v", p, e)
			}
		} else if e.BatchID != nil {
			t.Fatalf("new evidence combined with incomplete progress: %+v %+v", p, e)
		}
		select {
		case err = <-result:
			if err != nil {
				t.Fatal(err)
			}
			return
		default:
		}
	}
}
