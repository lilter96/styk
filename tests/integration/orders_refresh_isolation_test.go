//go:build integration

package integration

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
)

type refreshIsolationProvider struct {
	discovery *orders.Posting
	postings  map[string]orders.Posting
	fail      bool
	calls     []string
}

func (p *refreshIsolationProvider) ReadOrderPage(context.Context, orders.Scope, orders.OrderWindow, string) (orders.OrderSourcePage, error) {
	if p.discovery == nil {
		return orders.OrderSourcePage{}, orders.ErrBlocked
	}
	return orders.OrderSourcePage{Postings: []orders.Posting{*p.discovery}, Complete: true}, nil
}
func (p *refreshIsolationProvider) ReadOrderPosting(_ context.Context, scope orders.Scope, number string) (orders.Posting, error) {
	p.calls = append(p.calls, number)
	if p.fail && number == "a-unavailable" {
		return orders.Posting{}, orders.ErrNotFound
	}
	posting := p.postings[number]
	if posting.Scope != scope {
		return orders.Posting{}, orders.ErrConflict
	}
	return posting, nil
}

func TestOrdersActiveRefreshFailureIsolationAndRecoveryPostgres(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := ordersStorePosting(fixture.config.Scope, "a-unavailable", "a-initial", now)
	second := ordersStorePosting(fixture.config.Scope, "b-independent", "b-initial", now)
	ordersStoreCommit(t, fixture, first, "seed-a")
	ordersStoreCommit(t, fixture, second, "seed-b")
	first.Evidence.Revision = "a-refreshed"
	second.Evidence.Revision = "b-refreshed"
	first.Evidence.ProviderUpdatedAt = orders.Present(now.Add(time.Second))
	second.Evidence.ProviderUpdatedAt = orders.Present(now.Add(time.Second))
	provider := &refreshIsolationProvider{postings: map[string]orders.Posting{first.PostingNumber: first, second.PostingNumber: second}, fail: true}
	ingestor, err := orderflow.NewIngestor(fixture.db, provider, orderflow.IngestionOptions{WorkerID: "refresh-isolation", ActiveBatch: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err = ingestor.CollectStream(fixture.ctx, fixture.config.Scope, orderflow.ActiveStream); err != nil {
		t.Fatal(err)
	}
	partial, err := fixture.db.OrderCheckpoint(fixture.ctx, fixture.config.Scope, orderflow.ActiveStream)
	if err != nil || partial.Complete || !partial.Exhausted || partial.Cursor != "" || len(partial.Failures) != 1 || partial.Failures[0].PostingNumber != first.PostingNumber {
		t.Fatalf("partial coverage not persisted: %+v %v", partial, err)
	}
	var refreshed int
	if err = fixture.db.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM orders_posting_observations WHERE source_revision='b-refreshed'`).Scan(&refreshed); err != nil || refreshed != 1 {
		t.Fatalf("independent successful fact lost: %d %v", refreshed, err)
	}
	provider.fail = false
	if err = ingestor.CollectStream(fixture.ctx, fixture.config.Scope, orderflow.ActiveStream); err != nil {
		t.Fatal(err)
	}
	complete, err := fixture.db.OrderCheckpoint(fixture.ctx, fixture.config.Scope, orderflow.ActiveStream)
	if err != nil || !complete.Complete || !complete.Exhausted || len(complete.Failures) != 0 || complete.RefreshKey == partial.RefreshKey || complete.Revision != partial.Revision+1 {
		t.Fatalf("failed key not retried in new epoch: %+v %v", complete, err)
	}
	if err = fixture.db.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM orders_posting_observations WHERE source_revision='a-refreshed'`).Scan(&refreshed); err != nil || refreshed != 1 {
		t.Fatalf("recovered fact missing: %d %v", refreshed, err)
	}
	var historical []byte
	if err = fixture.db.Pool.QueryRow(fixture.ctx, `SELECT checkpoint FROM orders_ingestion_pages WHERE stream=$1 AND checkpoint->>'refresh_key'=$2`, orderflow.ActiveStream, partial.RefreshKey).Scan(&historical); err != nil {
		t.Fatal(err)
	}
	if len(historical) == 0 {
		t.Fatal("partial failure history overwritten")
	}
}

func TestOrdersIngestorSealsPIIWithActualTraversalTimestamp(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	config := fixture.config
	config.PIIEnabled, config.PIIRetentionSeconds = true, 3600
	var err error
	fixture.config, err = fixture.db.SaveOrderConfig(fixture.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: fixture.session.AuditID, IdempotencyKey: "ingestor-pii-config"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond).Add(123 * time.Nanosecond)
	posting := ordersStorePosting(fixture.config.Scope, "pii-ingested", "pii-source", now)
	posting.PII = &orders.Customer{Address: orders.Present(orders.Address{City: orders.Present("SYNTHETIC-PRIVATE-INGESTED-CITY")})}
	provider := &refreshIsolationProvider{discovery: &posting}
	protector := structuredAddressProtector(t, fixture)
	ingestor, err := orderflow.NewIngestor(fixture.db, provider, orderflow.IngestionOptions{WorkerID: "ingestor-pii", Protector: protector, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if err = ingestor.CollectStream(fixture.ctx, fixture.config.Scope, orderflow.DiscoveryStream); err != nil {
		t.Fatal(err)
	}
	page, err := fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, orders.PageRequest{Limit: 10})
	if err != nil || len(page.Postings) != 1 {
		t.Fatalf("read sealed posting: %v", err)
	}
	actual := page.Postings[0]
	if !actual.Evidence.ObservedAt.Equal(now) || actual.Customer.Revision < 1 || actual.PII != nil {
		t.Fatal("source timestamp or protected customer lost")
	}
	secret, err := fixture.db.OrderSecret(fixture.ctx, fixture.config.Scope, actual.PostingNumber, actual.Customer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !secret.CreatedAt.Equal(now.Truncate(time.Microsecond)) || !secret.ExpiresAt.After(time.Now()) || secret.ExpiresAt.Sub(secret.CreatedAt) != time.Hour || bytes.Contains(secret.Ciphertext, []byte("SYNTHETIC-PRIVATE-INGESTED-CITY")) {
		t.Fatal("real ingestion produced expired, unaligned or plaintext secret")
	}
	customer, err := protector.Customer(fixture.ctx, fixture.config.Scope, actual.PostingNumber, actual.Customer)
	if err != nil || !customer.Address.IsKnown() || !customer.Address.Value.City.IsKnown() || *customer.Address.Value.City.Value != "SYNTHETIC-PRIVATE-INGESTED-CITY" {
		t.Fatalf("protected customer could not be read: %v", err)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_posting_heads WHERE projection::text LIKE '%SYNTHETIC-PRIVATE-INGESTED-CITY%'`, 0)
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_posting_observations WHERE posting::text LIKE '%SYNTHETIC-PRIVATE-INGESTED-CITY%'`, 0)
}

func TestOrdersActiveRefreshEarlierPageFailureRemainsPartialPostgres(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := ordersStorePosting(fixture.config.Scope, "a-unavailable", "a-initial", now)
	second := ordersStorePosting(fixture.config.Scope, "b-independent", "b-initial", now)
	ordersStoreCommit(t, fixture, first, "seed-a")
	ordersStoreCommit(t, fixture, second, "seed-b")
	provider := &refreshIsolationProvider{postings: map[string]orders.Posting{first.PostingNumber: first, second.PostingNumber: second}, fail: true}
	ingestor, err := orderflow.NewIngestor(fixture.db, provider, orderflow.IngestionOptions{WorkerID: "refresh-pages", ActiveBatch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = ingestor.CollectStream(fixture.ctx, fixture.config.Scope, orderflow.ActiveStream); err != nil {
		t.Fatal(err)
	}
	initial, err := fixture.db.OrderCheckpoint(fixture.ctx, fixture.config.Scope, orderflow.ActiveStream)
	if err != nil || initial.Complete || initial.Exhausted || initial.Cursor != first.PostingNumber || len(initial.Failures) != 1 {
		t.Fatalf("partial first page invalid: %+v %v", initial, err)
	}
	if err = ingestor.CollectStream(fixture.ctx, fixture.config.Scope, orderflow.ActiveStream); err != nil {
		t.Fatal(err)
	}
	final, err := fixture.db.OrderCheckpoint(fixture.ctx, fixture.config.Scope, orderflow.ActiveStream)
	if err != nil || final.Complete || !final.Exhausted || final.Cursor != "" || len(final.Failures) != 1 || final.RefreshKey != initial.RefreshKey {
		t.Fatalf("last successful page hid earlier failure: %+v %v", final, err)
	}
}
