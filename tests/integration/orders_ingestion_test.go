//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersStoreIngestionPageCheckpointAtomicity(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	lease := ordersIngestionLease(t, fixture, "window", "refresh-1", time.Minute)
	first := ordersStorePosting(fixture.config.Scope, "page-1", "revision-1", now)
	page := orders.SourcePage{Scope: fixture.config.Scope, ConfigRevision: lease.ConfigRevision, Credentials: lease.Credentials, Stream: "window", RefreshKey: "refresh-1", NextCursor: "cursor-1", WindowFrom: now.Add(-time.Hour), WindowTo: now, Postings: []orders.Posting{first}, StartedAt: now, FinishedAt: now, IdempotencyKey: "page-first", FenceToken: lease.Token}
	checkpoint, err := fixture.db.CommitOrderPage(fixture.ctx, page)
	if err != nil || checkpoint.Revision != 1 || checkpoint.Complete {
		t.Fatalf("partial page checkpoint: %+v %v", checkpoint, err)
	}
	second := ordersStorePosting(fixture.config.Scope, "page-2", "revision-2", now)
	page.Postings = []orders.Posting{second}
	page.ExpectedCheckpointRevision, page.ExpectedCursor = 1, "wrong-cursor"
	page.NextCursor, page.Complete, page.IdempotencyKey = "", true, "page-second"
	if _, err = fixture.db.CommitOrderPage(fixture.ctx, page); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("stale paging cursor accepted: %v", err)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_posting_identities WHERE posting_number='page-2'`, 0)
	page.ExpectedCursor = "cursor-1"
	checkpoint, err = fixture.db.CommitOrderPage(fixture.ctx, page)
	if err != nil || !checkpoint.Complete || checkpoint.Revision != 2 {
		t.Fatalf("complete page checkpoint: %+v %v", checkpoint, err)
	}
	replay, err := fixture.db.CommitOrderPage(fixture.ctx, page)
	if err != nil || replay.Revision != checkpoint.Revision {
		t.Fatalf("page replay advanced checkpoint: %+v %v", replay, err)
	}
	page.Postings[0].Lines[0].Quantity = "3"
	if _, err = fixture.db.CommitOrderPage(fixture.ctx, page); !errors.Is(err, orders.ErrConflict) {
		t.Fatalf("reused page key changed facts: %v", err)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_posting_observations`, 2)
}

func TestOrdersStoreSerialDetailUnknownOrderingAndQuarantine(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	posting := ordersStorePosting(fixture.config.Scope, "serial-posting", "first", at)
	posting.Evidence.ProviderUpdatedAt = orders.Field[time.Time]{Presence: orders.PresenceMissing}
	ordersStoreCommit(t, fixture, posting, "serial-first")
	posting.Status, posting.Evidence.Revision = orders.Present("delivering"), "second"
	posting.Evidence.ObservedAt = at.Add(time.Minute)
	ordersStoreCommit(t, fixture, posting, "serial-second")
	page, err := fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil || len(page.Postings) != 1 || page.Postings[0].Evidence.Revision != "second" {
		t.Fatalf("serialized fresh detail did not advance unknown chronology: %+v %v", page, err)
	}
	posting.Status, posting.Evidence.Revision = orders.Present("awaiting_deliver"), "first"
	posting.Evidence.ObservedAt = at.Add(2 * time.Minute)
	ordersStoreCommit(t, fixture, posting, "serial-reappeared")
	page, err = fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil || page.Postings[0].Evidence.Revision != "second" || len(page.Postings[0].Drift) != 1 || page.Postings[0].Drift[0].Code != "ORDERING_SUPERSEDED_OBSERVATION" {
		t.Fatalf("superseded source facts rolled back head: %+v %v", page, err)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_posting_conflicts`, 1)
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_posting_observations`, 2)
}

func TestOrdersStoreDatedFactsAndFreshness(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	posting := ordersStorePosting(fixture.config.Scope, "dated-posting", "newer", at.Add(time.Minute))
	posting.Status = orders.Present("delivering")
	ordersStoreCommit(t, fixture, posting, "dated-newer")
	posting.Status, posting.Evidence.Revision = orders.Present("awaiting_deliver"), "older"
	posting.Evidence.ProviderUpdatedAt = orders.Present(at)
	posting.Evidence.ObservedAt = at.Add(2 * time.Minute)
	ordersStoreCommit(t, fixture, posting, "dated-older")
	page, err := fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil || page.Postings[0].Evidence.Revision != "newer" {
		t.Fatalf("late older fact replaced newer head: %+v %v", page, err)
	}
	before := page.Postings[0].Evidence
	posting.Status, posting.Evidence.Revision = orders.Present("delivering"), "newer"
	posting.Evidence.ProviderUpdatedAt = orders.Present(at.Add(time.Minute))
	posting.Evidence.ObservedAt = time.Now().UTC()
	ordersStoreCommit(t, fixture, posting, "dated-recheck")
	page, err = fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil || !page.Postings[0].Evidence.ObservedAt.Equal(before.ObservedAt) || !page.Postings[0].Evidence.CheckedAt.After(before.CheckedAt) {
		t.Fatalf("fresh recheck rewrote original observation timestamp: %+v %v", page, err)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_posting_observations`, 2)
}

func TestOrdersStoreIngestionFenceReconfigurationAndOldResponse(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	lease := ordersIngestionLease(t, fixture, "old-stream", "old-refresh", time.Minute)
	if newer, err := fixture.db.ClaimOrderIngestion(fixture.ctx, orders.IngestionClaimRequest{Scope: fixture.config.Scope, Stream: "different-stream", RefreshKey: "other", WorkerID: "other", Now: time.Now(), LeaseUntil: time.Now().Add(time.Minute)}); err != nil || newer != nil {
		t.Fatalf("parallel scope collector acquired lease: %+v %v", newer, err)
	}
	if err := fixture.db.ReleaseOrderIngestion(fixture.ctx, lease); err != nil {
		t.Fatal(err)
	}
	newer := ordersIngestionLease(t, fixture, "old-stream", "new-refresh", time.Minute)
	now := time.Now().UTC().Truncate(time.Microsecond)
	page := orders.SourcePage{Scope: fixture.config.Scope, ConfigRevision: lease.ConfigRevision, Credentials: lease.Credentials, Stream: "old-stream", RefreshKey: "old-refresh", Complete: true, WindowFrom: now.Add(-time.Hour), WindowTo: now, Postings: []orders.Posting{ordersStorePosting(fixture.config.Scope, "late-posting", "late", now)}, StartedAt: now, FinishedAt: now, IdempotencyKey: "late-page", FenceToken: lease.Token}
	if _, err := fixture.db.CommitOrderPage(fixture.ctx, page); !errors.Is(err, orders.ErrStale) {
		t.Fatalf("superseded HTTP response committed: %v", err)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_posting_observations`, 0)
	if err := fixture.db.ReleaseOrderIngestion(fixture.ctx, newer); err != nil {
		t.Fatal(err)
	}
}

func TestOrdersStoreActiveSetIgnoresCreationWindow(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	active := ordersStorePosting(fixture.config.Scope, "old-active", "active", now)
	active.Times.CreatedAt = orders.Present(now.Add(-365 * 24 * time.Hour))
	ordersStoreCommit(t, fixture, active, "active-first")
	terminal := ordersStorePosting(fixture.config.Scope, "terminal", "terminal", now)
	terminal.Status = orders.Present("delivered")
	ordersStoreCommit(t, fixture, terminal, "terminal-first")
	page, err := fixture.db.ActiveOrderPostings(fixture.ctx, fixture.config.Scope, 10, "")
	if err != nil || len(page.Postings) != 1 || page.Postings[0].PostingNumber != active.PostingNumber {
		t.Fatalf("active set lost old unfinished posting: %+v %v", page, err)
	}
}

func TestOrdersStoreProjectionKeepsKnownFactsWithoutRewritingObservation(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	posting := ordersStorePosting(fixture.config.Scope, "presence-posting", "known", now)
	ordersStoreCommit(t, fixture, posting, "presence-known")
	posting.OrderNumber = orders.Field[string]{Presence: orders.PresenceNull}
	posting.Lines[0].Price = orders.Field[orders.Money]{Presence: orders.PresenceMissing}
	posting.Evidence.Revision = "missing"
	posting.Evidence.ProviderUpdatedAt = orders.Present(now.Add(time.Second))
	ordersStoreCommit(t, fixture, posting, "presence-missing")
	page, err := fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil || !page.Postings[0].OrderNumber.IsKnown() || !page.Postings[0].Lines[0].Price.IsKnown() {
		t.Fatalf("missing provider fields erased known projection facts: %+v %v", page, err)
	}
	var presence string
	if err = fixture.db.Pool.QueryRow(fixture.ctx, `SELECT posting#>>'{order_number,presence}' FROM orders_posting_observations WHERE source_revision='missing'`).Scan(&presence); err != nil || presence != "null" {
		t.Fatalf("source observation presence was rewritten: %s %v", presence, err)
	}
}

func ordersIngestionLease(t testing.TB, fixture ordersStoreFixture, stream, key string, duration time.Duration) orders.IngestionLease {
	t.Helper()
	now := time.Now()
	lease, err := fixture.db.ClaimOrderIngestion(fixture.ctx, orders.IngestionClaimRequest{Scope: fixture.config.Scope, Stream: stream, RefreshKey: key, WorkerID: "orders-ingestion-test", Now: now, LeaseUntil: now.Add(duration)})
	if err != nil || lease == nil {
		t.Fatalf("claim ingestion lease: %v", err)
	}
	return *lease
}

func assertOrdersRowCount(t testing.TB, fixture ordersStoreFixture, query string, expected int) {
	t.Helper()
	var count int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, query).Scan(&count); err != nil || count != expected {
		t.Fatalf("orders evidence count=%d expected=%d error=%v", count, expected, err)
	}
}
