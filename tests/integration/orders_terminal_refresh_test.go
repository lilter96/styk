//go:build integration

package integration

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
)

func terminalRefreshSeed(t *testing.T, f ordersStoreFixture, number, status, scheme string, observed time.Time, checkedAge string) orders.Posting {
	t.Helper()
	posting := ordersStorePosting(f.config.Scope, number, "initial-"+number, observed)
	posting.Status, posting.Scheme = orders.Present(status), orders.Present(scheme)
	ordersStoreCommit(t, f, posting, "terminal-seed-"+number)
	terminalRefreshAge(t, f, number, checkedAge)
	return posting
}

func terminalRefreshAge(t *testing.T, f ordersStoreFixture, number, age string) {
	t.Helper()
	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE orders_posting_heads SET checked_at=clock_timestamp()-$5::interval WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=$4`, f.config.Scope.Tenant, f.config.Scope.MoyskladAccount, f.config.Scope.OzonAccount, number, age); err != nil {
		t.Fatal(err)
	}
}

func terminalRefreshNumbers(t *testing.T, f ordersStoreFixture, limit int, after string) ([]string, string) {
	t.Helper()
	page, err := f.db.ActiveOrderPostings(f.ctx, f.config.Scope, limit, after)
	if err != nil {
		t.Fatal(err)
	}
	numbers := make([]string, 0, len(page.Postings))
	for _, posting := range page.Postings {
		numbers = append(numbers, posting.PostingNumber)
	}
	return numbers, page.NextCursor
}

func TestOrdersTerminalRefreshUsesKnownSourceStatusDueTimeAndRetention(t *testing.T) {
	f := newOrdersStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		name, status, scheme, age string
		observed                  time.Time
	}{
		{"a-old-active", "awaiting_deliver", "FBS", "1 minute", now.Add(-60 * 24 * time.Hour)},
		{"b-due-delivered", "delivered", "FBS", "7 hours", now.Add(-30 * 24 * time.Hour)},
		{"c-due-cancelled", "cancelled", "rFBS", "7 hours", now.Add(-24 * time.Hour)},
		{"d-due-not-accepted", "not_accepted", "FBS", "7 hours", now.Add(-24 * time.Hour)},
		{"e-notdue-delivered", "delivered", "FBS", "5 hours", now.Add(-24 * time.Hour)},
		{"f-notdue-not-accepted", "not_accepted", "FBS", "5 hours", now.Add(-24 * time.Hour)},
		{"g-expired-terminal", "cancelled", "FBS", "7 hours", now.Add(-32 * 24 * time.Hour)},
		{"h-new-status-active", "new_provider_status", "FBS", "1 minute", now.Add(-24 * time.Hour)},
		{"i-fbo-active", "awaiting_deliver", "FBO", "7 hours", now.Add(-24 * time.Hour)},
		{"j-fbo-terminal", "cancelled", "FBO", "7 hours", now.Add(-24 * time.Hour)},
	} {
		terminalRefreshSeed(t, f, tc.name, tc.status, tc.scheme, tc.observed, tc.age)
	}
	numbers, _ := terminalRefreshNumbers(t, f, 100, "")
	expected := []string{"a-old-active", "b-due-delivered", "c-due-cancelled", "d-due-not-accepted", "f-notdue-not-accepted", "h-new-status-active"}
	if !reflect.DeepEqual(numbers, expected) {
		t.Fatalf("terminal refresh selection=%v want=%v", numbers, expected)
	}
}

func TestOrdersTerminalRefreshUsesActualStatusAndOnlyReadsUnknownScheme(t *testing.T) {
	f := newOrdersStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, presence := range []orders.Presence{orders.PresenceMissing, orders.PresenceNull, orders.PresenceEmpty} {
		number := "retained-" + string(presence)
		posting := terminalRefreshSeed(t, f, number, "cancelled", "FBS", now.Add(-24*time.Hour), "7 hours")
		posting.Status = orders.Field[string]{Presence: presence}
		posting.Evidence.Revision = "unknown-" + number
		posting.Evidence.ObservedAt = now.Add(-12 * time.Hour)
		posting.Evidence.ProviderUpdatedAt = orders.Present(posting.Evidence.ObservedAt)
		ordersStoreCommit(t, f, posting, "terminal-unknown-"+number)
		terminalRefreshAge(t, f, number, "7 hours")
	}
	posting := terminalRefreshSeed(t, f, "unknown-scheme", "cancelled", "FBS", now.Add(-24*time.Hour), "7 hours")
	posting.Scheme = orders.Field[string]{Presence: orders.PresenceNull}
	posting.Evidence.Revision = "unknown-scheme-current"
	posting.Evidence.ObservedAt = now.Add(-12 * time.Hour)
	posting.Evidence.ProviderUpdatedAt = orders.Present(posting.Evidence.ObservedAt)
	ordersStoreCommit(t, f, posting, "terminal-unknown-scheme")
	terminalRefreshAge(t, f, posting.PostingNumber, "7 hours")
	never := ordersStorePosting(f.config.Scope, "never-known-scheme", "never-known", now.Add(-24*time.Hour))
	never.Status = orders.Present("cancelled")
	never.Scheme = orders.Field[string]{Presence: orders.PresenceMissing}
	ordersStoreCommit(t, f, never, "terminal-never-known")
	terminalRefreshAge(t, f, never.PostingNumber, "7 hours")
	numbers, _ := terminalRefreshNumbers(t, f, 100, "")
	if !reflect.DeepEqual(numbers, []string{"never-known-scheme", "unknown-scheme"}) {
		t.Fatalf("retained source status was treated as current terminal: %v", numbers)
	}
	page, err := f.db.ActiveOrderPostings(f.ctx, f.config.Scope, 100, "")
	if err != nil || page.Postings[0].Scheme.IsKnown() {
		t.Fatal("classification refresh fabricated seller-fulfilled scheme")
	}

	var knownProjection int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_posting_heads WHERE projection#>>'{status,value}'='cancelled'`).Scan(&knownProjection); err != nil || knownProjection != 5 {
		t.Fatal("fixture did not preserve prior known business state")
	}
}

func TestOrdersTerminalRefreshSharesLexicalCursorAndAccountScope(t *testing.T) {
	f := newOrdersStoreFixture(t)
	now := time.Now().UTC().Add(-24 * time.Hour)
	for _, tc := range []struct{ number, status string }{{"a", "awaiting_deliver"}, {"b", "cancelled"}, {"c", "not_accepted"}, {"d", "delivering"}} {
		terminalRefreshSeed(t, f, tc.number, tc.status, "FBS", now, "7 hours")
	}
	first, next := terminalRefreshNumbers(t, f, 2, "")
	second, end := terminalRefreshNumbers(t, f, 2, next)
	if !reflect.DeepEqual(first, []string{"a", "b"}) || next != "b" || !reflect.DeepEqual(second, []string{"c", "d"}) || end != "" {
		t.Fatalf("mixed active/terminal cursor lost keys: %v %q %v %q", first, next, second, end)
	}
	for _, scope := range []orders.Scope{{Tenant: "other", MoyskladAccount: f.config.Scope.MoyskladAccount, OzonAccount: f.config.Scope.OzonAccount}, {Tenant: f.config.Scope.Tenant, MoyskladAccount: "other", OzonAccount: f.config.Scope.OzonAccount}, {Tenant: f.config.Scope.Tenant, MoyskladAccount: f.config.Scope.MoyskladAccount, OzonAccount: "other"}} {
		page, err := f.db.ActiveOrderPostings(f.ctx, scope, 100, "")
		if err != nil || len(page.Postings) != 0 {
			t.Fatalf("cross-scope terminal refresh returned data: %v", err)
		}
	}
}

type terminalRefreshReader struct {
	posting          orders.Posting
	entered, release chan struct{}
	once             sync.Once
	reads            atomic.Int32
}

func (r *terminalRefreshReader) ReadOrderPage(context.Context, orders.Scope, orders.OrderWindow, string) (orders.OrderSourcePage, error) {
	return orders.OrderSourcePage{}, orders.ErrBlocked
}
func (r *terminalRefreshReader) ReadOrderPosting(ctx context.Context, scope orders.Scope, number string) (orders.Posting, error) {
	r.reads.Add(1)
	r.once.Do(func() { close(r.entered) })
	select {
	case <-ctx.Done():
		return orders.Posting{}, ctx.Err()
	case <-r.release:
	}
	if r.posting.Scope != scope || r.posting.PostingNumber != number {
		return orders.Posting{}, orders.ErrInvalid
	}
	return r.posting, nil
}

func TestOrdersTerminalRefreshUsesExistingLeaseAndCanBecomeActiveAgain(t *testing.T) {
	f := newOrdersStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	posting := terminalRefreshSeed(t, f, "corrected-terminal", "cancelled", "FBS", now.Add(-24*time.Hour), "7 hours")
	posting.Status = orders.Present("delivering")
	posting.Evidence.Revision = "corrected-active"
	posting.Evidence.ObservedAt = now
	posting.Evidence.ProviderUpdatedAt = orders.Present(now)
	reader := &terminalRefreshReader{posting: posting, entered: make(chan struct{}), release: make(chan struct{})}
	first, err := orderflow.NewIngestor(f.db, reader, orderflow.IngestionOptions{WorkerID: "terminal-reader-first", LeaseDuration: time.Minute, ActiveBatch: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := orderflow.NewIngestor(f.db, reader, orderflow.IngestionOptions{WorkerID: "terminal-reader-second", LeaseDuration: time.Minute, ActiveBatch: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- first.CollectStream(ctx, f.config.Scope, orderflow.ActiveStream) }()
	select {
	case <-reader.entered:
	case <-ctx.Done():
		t.Fatal("terminal reader never claimed existing stream lease")
	}
	if err = second.CollectStream(ctx, f.config.Scope, orderflow.ActiveStream); err != nil {
		t.Fatal(err)
	}
	if reader.reads.Load() != 1 {
		t.Fatal("concurrent refresh readers duplicated a provider detail call")
	}
	close(reader.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	checkpoint, err := f.db.OrderCheckpoint(f.ctx, f.config.Scope, orderflow.ActiveStream)
	if err != nil || !checkpoint.Complete || checkpoint.Revision != 1 {
		t.Fatalf("terminal correction checkpoint not atomic: %v", err)
	}
	page, err := f.db.ActiveOrderPostings(f.ctx, f.config.Scope, 10, "")
	if err != nil || len(page.Postings) != 1 || !page.Postings[0].Status.IsKnown() || *page.Postings[0].Status.Value != "delivering" {
		t.Fatal("corrected terminal was not restored to ordinary active refresh")
	}
}

type terminalClassificationReader struct{ postings map[string]orders.Posting }

func (r terminalClassificationReader) ReadOrderPage(context.Context, orders.Scope, orders.OrderWindow, string) (orders.OrderSourcePage, error) {
	return orders.OrderSourcePage{}, orders.ErrBlocked
}
func (r terminalClassificationReader) ReadOrderPosting(_ context.Context, scope orders.Scope, number string) (orders.Posting, error) {
	posting, found := r.postings[number]
	if !found || posting.Scope != scope {
		return orders.Posting{}, orders.ErrNotFound
	}
	return posting, nil
}

type terminalClassificationResolver struct{}

func (terminalClassificationResolver) ResolveOrder(context.Context, orders.Scope, string, string, int64) (orders.Binding, error) {
	return orders.Binding{}, orders.ErrBlocked
}

func TestOrdersTerminalRefreshFBOClassificationDoesNotStarveIndependentKeys(t *testing.T) {
	f := newOrdersStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	source := map[string]orders.Posting{}
	for _, number := range []string{"a-classify-fbo", "b-independent-fbs"} {
		posting := ordersStorePosting(f.config.Scope, number, "initial-"+number, now.Add(-24*time.Hour))
		posting.Status = orders.Present("cancelled")
		posting.Scheme = orders.Field[string]{Presence: orders.PresenceMissing}
		ordersStoreCommit(t, f, posting, "terminal-classify-"+number)
		terminalRefreshAge(t, f, number, "7 hours")
		posting.Evidence.Revision = "classified-" + number
		posting.Evidence.ObservedAt = now
		posting.Evidence.ProviderUpdatedAt = orders.Present(now)
		posting.Status = orders.Present("awaiting_deliver")
		posting.Scheme = orders.Present("FBS")
		if number == "a-classify-fbo" {
			posting.Scheme = orders.Present("FBO")
		}
		source[number] = posting
	}
	collector, err := orderflow.NewIngestor(f.db, terminalClassificationReader{source}, orderflow.IngestionOptions{WorkerID: "terminal-classifier", LeaseDuration: time.Minute, ActiveBatch: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err = collector.CollectStream(f.ctx, f.config.Scope, orderflow.ActiveStream); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := f.db.OrderCheckpoint(f.ctx, f.config.Scope, orderflow.ActiveStream)
	if err != nil || !checkpoint.Complete {
		t.Fatal("one classified FBO prevented durable refresh of independent seller-fulfilled posting")
	}
	numbers, _ := terminalRefreshNumbers(t, f, 100, "")
	if !reflect.DeepEqual(numbers, []string{"b-independent-fbs"}) {
		t.Fatalf("known FBO remained in automatic workflow: %v", numbers)
	}
	service, err := orderflow.NewService(f.db, terminalClassificationResolver{}, orderflow.ServiceOptions{AllowScope: func(s orders.Scope) bool { return s == f.config.Scope }})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Preview(f.ctx, orders.PreviewRequest{Scope: f.config.Scope, ExpectedRevision: f.config.Revision, Actor: f.session.AuditID, PostingNumbers: []string{"a-classify-fbo"}})
	if err != nil || len(preview.Decisions) != 1 || preview.Decisions[0].Ready || len(preview.Decisions[0].Commands) > 0 {
		t.Fatal("classified FBO produced a native mirror decision")
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_work`, 0)
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_posting_heads WHERE projection#>>'{evidence,revision}' LIKE 'classified-%'`, 2)
}
