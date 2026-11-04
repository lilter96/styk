//go:build integration

package postgres

import (
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestFulfillmentLateUnversionedReadbackCannotBorrowNewAdmissionSequence(t *testing.T) {
	f := orderInventoryDBFixture(t)
	before, late, work := readbackTestValues(t, f)
	idleFulfillmentCollector(t, &f)
	latest := late
	latest.Lines = slices.Clone(late.Lines)
	latest.Lines[0].Quantity = "1"
	latest.Evidence.Revision = "independent-newer-unversioned-detail"
	latest.Evidence.ProviderUpdatedAt = orders.Field[time.Time]{}
	latest.Evidence.CheckedAt = f.now.Add(-time.Minute)
	latest.Evidence.ObservedAt = latest.Evidence.CheckedAt
	if err := saveReadbackTest(t, f, work, orders.Completion{State: orders.StateReconciled, ObservedPosting: &latest}); err != nil {
		t.Fatal("admit genuine serial point observation", err)
	}
	late.Evidence.ProviderUpdatedAt = orders.Field[time.Time]{}
	if err := saveReadbackTest(t, f, work, orders.Completion{State: orders.StateReconciled, ObservedPosting: &late}); err != nil {
		t.Fatal("retain historical late readback", err)
	}
	current, err := readOrderPosting(f.ctx, f.db.Pool, work.Scope, before.PostingNumber)
	if err != nil || current.Evidence.Revision != latest.Evidence.Revision || !current.Evidence.CheckedAt.Equal(latest.Evidence.CheckedAt) || current.Lines[0].Quantity != "1" {
		t.Fatal("late response borrowed a fresh local sequence to overwrite current facts", err)
	}
	var kind string
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT posting#>>'{evidence,collection_kind}' FROM orders_posting_observations WHERE source_revision=$1`, late.Evidence.Revision).Scan(&kind); err != nil || kind != string(orders.CollectionSnapshot) {
		t.Fatal("stale command response acquired serial authority", err)
	}
}

func idleFulfillmentCollector(t *testing.T, f *orderInventoryFixture) {
	t.Helper()
	enabled := f.config
	enabled.Enabled, enabled.TestEnabled = true, true
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: enabled, ExpectedRevision: f.config.Revision, Actor: "user." + "aaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "adversarial-collector-enable"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.db.ClaimOrderIngestion(f.ctx, orders.IngestionClaimRequest{Scope: f.config.Scope, Stream: "detail", RefreshKey: "adversarial-detail", WorkerID: "adversarial-collector", Now: time.Now(), LeaseUntil: time.Now().Add(time.Minute)})
	if err != nil || lease == nil {
		t.Fatal("create normal durable collection clock", err)
	}
	if err = f.db.ReleaseOrderIngestion(f.ctx, *lease); err != nil {
		t.Fatal("leave collector genuinely idle", err)
	}
}
