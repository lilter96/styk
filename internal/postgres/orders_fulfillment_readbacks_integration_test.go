//go:build integration

package postgres

import (
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func saveReadbackTest(t *testing.T, f orderInventoryFixture, work orders.Work, completion orders.Completion) error {
	t.Helper()
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	if err = ordersLock(f.ctx, tx, work.Scope); err != nil {
		t.Fatal(err)
	}
	now, err := ordersClock(f.ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err = commitFulfillmentReadbackFacts(f.ctx, tx, work, completion, now); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}

func readbackTestValues(t *testing.T, f orderInventoryFixture) (orders.Posting, orders.Posting, orders.Work) {
	t.Helper()
	before := orderInventoryPosting(t, f, "readback-posting", "awaiting_packaging")
	after := before
	after.Lines = slices.Clone(before.Lines)
	after.Lines[0].Quantity = "2"
	after.Evidence.Revision = "after-partial"
	after.Evidence.ProviderUpdatedAt = orders.Present(f.now.Add(-4 * time.Minute))
	after.Evidence.ObservedAt = f.now.Add(-3 * time.Minute)
	after.Evidence.CheckedAt = after.Evidence.ObservedAt
	work := orders.Work{ID: "readback-work", Scope: before.Scope, Command: orders.Command{Scope: before.Scope, Action: orders.ActionPostingPartialCancel, PostingNumber: before.PostingNumber, CommercialOrderID: before.CommercialOrderID, ExpectedSourceRevision: before.Evidence.Revision}}
	return before, after, work
}

func TestFulfillmentReadbackActualCheckTimeAndReplayRemainTruthful(t *testing.T) {
	f := orderInventoryDBFixture(t)
	_, after, work := readbackTestValues(t, f)
	done := orders.Completion{State: orders.StateReconciled, ObservedPosting: &after}
	if err := saveReadbackTest(t, f, work, done); err != nil {
		t.Fatal(err)
	}
	current, err := readOrderPosting(f.ctx, f.db.Pool, work.Scope, after.PostingNumber)
	if err != nil || current.Evidence.Revision != after.Evidence.Revision || !current.Evidence.CheckedAt.Equal(after.Evidence.CheckedAt) || !current.Evidence.ObservedAt.Equal(after.Evidence.ObservedAt) {
		t.Fatalf("actual point read timestamps not retained: %+v %v", current.Evidence, err)
	}
	replay := after
	replay.Evidence.CheckedAt = replay.Evidence.CheckedAt.Add(-time.Minute)
	done.ObservedPosting = &replay
	if err = saveReadbackTest(t, f, work, done); err != nil {
		t.Fatal(err)
	}
	current, err = readOrderPosting(f.ctx, f.db.Pool, work.Scope, after.PostingNumber)
	if err != nil || !current.Evidence.CheckedAt.Equal(after.Evidence.CheckedAt) {
		t.Fatal("old replay refreshed or decreased actual check time", err)
	}
	var observed, created time.Time
	var count int
	err = f.db.Pool.QueryRow(f.ctx, `SELECT min(observed_at),min(created_at),count(*) FROM orders_posting_observations WHERE source_revision=$1`, after.Evidence.Revision).Scan(&observed, &created, &count)
	if err != nil || count != 1 || !observed.Equal(after.Evidence.ObservedAt) || created.Before(f.now) {
		t.Fatal("immutable observation or DB admission timestamp changed", err)
	}
	var checkpoints int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_checkpoints`).Scan(&checkpoints); err != nil || checkpoints != 0 {
		t.Fatal("point read fabricated complete traversal coverage", err)
	}
}

func TestFulfillmentReadbackDoesNotRollbackNewerProviderObservation(t *testing.T) {
	f := orderInventoryDBFixture(t)
	_, after, work := readbackTestValues(t, f)
	newer := after
	newer.Lines = slices.Clone(after.Lines)
	newer.Lines[0].Quantity = "1"
	newer.Evidence.Revision = "newer-independent-observation"
	newer.Evidence.ProviderUpdatedAt = orders.Present(f.now.Add(-time.Minute))
	newer.Evidence.ObservedAt = f.now.Add(-time.Minute)
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	if _, err = insertOrderPosting(f.ctx, tx, newer, f.now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = saveReadbackTest(t, f, work, orders.Completion{State: orders.StateReconciled, ObservedPosting: &after}); err != nil {
		t.Fatal(err)
	}
	current, err := readOrderPosting(f.ctx, f.db.Pool, work.Scope, after.PostingNumber)
	if err != nil || current.Evidence.Revision != newer.Evidence.Revision || !current.Evidence.CheckedAt.Equal(f.now) {
		t.Fatal("late readback replaced newer provider evidence", err)
	}
}

func TestFulfillmentReadbackActiveCollectorPreventsFalseSerialEvidence(t *testing.T) {
	f := orderInventoryDBFixture(t)
	before, after, work := readbackTestValues(t, f)
	enabled := f.config
	enabled.Enabled, enabled.TestEnabled = true, true
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: enabled, ExpectedRevision: f.config.Revision, Actor: "user." + "aaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "readback-collector-enable"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.db.ClaimOrderIngestion(f.ctx, orders.IngestionClaimRequest{Scope: work.Scope, Stream: "detail", RefreshKey: "concurrent-detail", WorkerID: "readback-test-collector", Now: time.Now(), LeaseUntil: time.Now().Add(time.Minute)})
	if err != nil || lease == nil {
		t.Fatal("claim genuine collector lease", err)
	}
	after.Evidence.ProviderUpdatedAt = orders.Field[time.Time]{}
	if err = saveReadbackTest(t, f, work, orders.Completion{State: orders.StateReconciled, ObservedPosting: &after}); err != nil {
		t.Fatal(err)
	}
	current, err := readOrderPosting(f.ctx, f.db.Pool, work.Scope, after.PostingNumber)
	if err != nil || current.Evidence.Revision != before.Evidence.Revision {
		t.Fatal("unversioned readback bypassed concurrent collector", err)
	}
	var kind, token string
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT posting#>>'{evidence,collection_kind}' FROM orders_posting_observations WHERE source_revision=$1`, after.Evidence.Revision).Scan(&kind); err != nil || kind != string(orders.CollectionSnapshot) {
		t.Fatal("point read masqueraded as serial traversal", err)
	}
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT token FROM orders_ingestion_leases`).Scan(&token); err != nil || token != lease.Token {
		t.Fatal("readback stole collector lease", err)
	}
}

func TestFulfillmentReadbackBatchFailureRollsBackEarlierPointObservations(t *testing.T) {
	f := orderInventoryDBFixture(t)
	before, after, work := readbackTestValues(t, f)
	work.Command.Action = orders.ActionPostingSplit
	foreign := after
	foreign.PostingNumber = "foreign-child"
	foreign.Scope.Tenant = "different-merchant"
	done := orders.Completion{State: orders.StateReconciled, Receipt: orders.Receipt{ObservedPostings: []orders.Posting{after, foreign}}}
	if err := saveReadbackTest(t, f, work, done); err == nil {
		t.Fatal("foreign child accepted")
	}
	current, err := readOrderPosting(f.ctx, f.db.Pool, work.Scope, before.PostingNumber)
	if err != nil || current.Evidence.Revision != before.Evidence.Revision {
		t.Fatal("failed atomic batch advanced earlier head", err)
	}
	var count int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_posting_observations`).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed batch retained partial source observations", err)
	}
}
