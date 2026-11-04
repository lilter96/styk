package orderflow

import (
	"context"
	"errors"
	"github.com/lilter96/styk/internal/orders"
	"strconv"
	"testing"
)

type isolatedRefreshSource struct {
	base       *ingestionTestSource
	failures   map[string]error
	calls      []string
	crossScope bool
	cancel     context.CancelFunc
}

func (s *isolatedRefreshSource) ReadOrderPage(ctx context.Context, scope orders.Scope, window orders.OrderWindow, cursor string) (orders.OrderSourcePage, error) {
	return s.base.ReadOrderPage(ctx, scope, window, cursor)
}
func (s *isolatedRefreshSource) ReadOrderPosting(ctx context.Context, scope orders.Scope, number string) (orders.Posting, error) {
	s.calls = append(s.calls, number)
	if err := s.failures[number]; err != nil {
		if s.cancel != nil {
			s.cancel()
		}
		return orders.Posting{}, err
	}
	posting, err := s.base.ReadOrderPosting(ctx, scope, number)
	if s.crossScope && number == "a" {
		posting.Scope.Tenant = "another-tenant"
	}
	return posting, err
}

type refreshHTTPError int

func (e refreshHTTPError) Error() string           { return "untrusted sensitive provider error" }
func (e refreshHTTPError) ProviderReadStatus() int { return int(e) }

func TestActiveRefreshIsolatesFailureAndRetriesNewEpoch(t *testing.T) {
	t.Parallel()
	ingestor, store, base := newIngestionFixture(t)
	store.active = orders.PostingPage{Postings: []orders.Posting{{PostingNumber: "a"}, {PostingNumber: "b"}}}
	source := &isolatedRefreshSource{base: base, failures: map[string]error{"a": orders.ErrNotFound}}
	ingestor.source = source
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, ActiveStream); err != nil {
		t.Fatal(err)
	}
	first := store.checkpoints[ActiveStream]
	if first.Complete || !first.Exhausted || first.Cursor != "" || len(first.Failures) != 1 || first.Failures[0].Code != "POSTING_NOT_FOUND" || len(store.pages[0].Postings) != 1 || store.pages[0].Postings[0].PostingNumber != "b" {
		t.Fatalf("failure lost unrelated success: %+v", first)
	}
	source.failures = nil
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, ActiveStream); err != nil {
		t.Fatal(err)
	}
	next := store.checkpoints[ActiveStream]
	if !next.Complete || len(next.Failures) != 0 || next.RefreshKey == first.RefreshKey || len(store.pages[1].Postings) != 2 {
		t.Fatalf("exhausted partial traversal did not recover: %+v", next)
	}
}

func TestActiveRefreshCarriesEarlierFailureToFinalPage(t *testing.T) {
	t.Parallel()
	ingestor, store, base := newIngestionFixture(t)
	store.active = orders.PostingPage{Postings: []orders.Posting{{PostingNumber: "a"}}, NextCursor: "a"}
	ingestor.source = &isolatedRefreshSource{base: base, failures: map[string]error{"a": orders.ErrSchemaDrift}}
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, ActiveStream); err != nil {
		t.Fatal(err)
	}
	store.active = orders.PostingPage{Postings: []orders.Posting{{PostingNumber: "b"}}}
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, ActiveStream); err != nil {
		t.Fatal(err)
	}
	checkpoint := store.checkpoints[ActiveStream]
	if checkpoint.Complete || !checkpoint.Exhausted || len(checkpoint.Failures) != 1 || len(store.pages[1].Failures) != 1 {
		t.Fatalf("final success hid earlier missing key: %+v", checkpoint)
	}
}

func TestActiveRefreshAccountFailureStopsWithoutSkippingUnreadKeys(t *testing.T) {
	t.Parallel()
	for _, status := range []refreshHTTPError{403, 429, 503} {
		t.Run(strconv.Itoa(int(status)), func(t *testing.T) {
			ingestor, store, base := newIngestionFixture(t)
			store.active = orders.PostingPage{Postings: []orders.Posting{{PostingNumber: "a"}, {PostingNumber: "b"}}}
			source := &isolatedRefreshSource{base: base, failures: map[string]error{"a": status}}
			ingestor.source = source
			if err := ingestor.CollectStream(context.Background(), store.config.Scope, ActiveStream); err != nil {
				t.Fatal(err)
			}
			checkpoint := store.checkpoints[ActiveStream]
			if len(source.calls) != 1 || checkpoint.Cursor != "a" || checkpoint.Exhausted || checkpoint.Complete {
				t.Fatalf("unread keys skipped or provider hammered: %+v calls=%v", checkpoint, source.calls)
			}
		})
	}
}

func TestActiveRefreshRejectsCrossScopeAndPreservesOtherPosting(t *testing.T) {
	t.Parallel()
	ingestor, store, base := newIngestionFixture(t)
	store.active = orders.PostingPage{Postings: []orders.Posting{{PostingNumber: "a"}, {PostingNumber: "b"}}}
	ingestor.source = &isolatedRefreshSource{base: base, crossScope: true}
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, ActiveStream); err != nil {
		t.Fatal(err)
	}
	page := store.pages[0]
	if len(page.Postings) != 1 || page.Postings[0].PostingNumber != "b" || page.Failures[0].Code != "POSTING_SCOPE_MISMATCH" {
		t.Fatalf("foreign response persisted: %+v", page)
	}
}

func TestActiveRefreshCanceledReadPersistsAlreadyReadFacts(t *testing.T) {
	t.Parallel()
	ingestor, store, base := newIngestionFixture(t)
	store.active = orders.PostingPage{Postings: []orders.Posting{{PostingNumber: "a"}, {PostingNumber: "b"}, {PostingNumber: "c"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &isolatedRefreshSource{base: base, failures: map[string]error{"b": context.Canceled}, cancel: cancel}
	ingestor.source = source
	if err := ingestor.CollectStream(ctx, store.config.Scope, ActiveStream); err != nil {
		t.Fatal(err)
	}
	if len(store.pages) != 1 || len(store.pages[0].Postings) != 1 || store.pages[0].NextCursor != "b" || len(source.calls) != 2 {
		t.Fatal("canceled read discarded successful facts or continued external requests")
	}
}

func TestRefreshReadErrorDiagnosticsAreSanitized(t *testing.T) {
	t.Parallel()
	code, stop := refreshReadFailure(errors.New("buyer@example.test secret /private/url"))
	if code != "POSTING_READ_FAILED" || stop {
		t.Fatalf("unsafe error classification %q %v", code, stop)
	}
}
