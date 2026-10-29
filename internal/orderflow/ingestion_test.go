package orderflow

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

var errIngestionTestCommit = errors.New("synthetic commit failure")

type ingestionTestStore struct {
	config                      orders.Config
	credentials                 orders.CredentialRevisions
	checkpoints                 map[string]orders.Checkpoint
	lease                       *orders.IngestionLease
	active                      orders.PostingPage
	pages                       []orders.SourcePage
	claims, releases, renewals  int
	busy, lostLease, failCommit bool
}

func (s *ingestionTestStore) OrderConfig(context.Context, orders.Scope) (orders.Config, error) {
	return s.config, nil
}
func (s *ingestionTestStore) OrderCheckpoint(_ context.Context, scope orders.Scope, stream string) (orders.Checkpoint, error) {
	if checkpoint, ok := s.checkpoints[stream]; ok {
		return checkpoint, nil
	}
	return orders.Checkpoint{Scope: scope, Stream: stream}, nil
}
func (s *ingestionTestStore) ActiveOrderPostings(context.Context, orders.Scope, int, string) (orders.PostingPage, error) {
	return s.active, nil
}

func (s *ingestionTestStore) ClaimOrderIngestion(_ context.Context, request orders.IngestionClaimRequest) (*orders.IngestionLease, error) {
	s.claims++
	if s.busy {
		return nil, nil
	}
	s.lease = &orders.IngestionLease{Scope: request.Scope, Stream: request.Stream, RefreshKey: request.RefreshKey,
		Token: "lease-" + strconv.Itoa(s.claims), Sequence: int64(s.claims), ConfigRevision: s.config.Revision,
		Credentials: s.credentials, LeaseUntil: request.LeaseUntil}
	lease := *s.lease
	return &lease, nil
}

func (s *ingestionTestStore) RenewOrderIngestion(_ context.Context, lease orders.IngestionLease, _ time.Time) (bool, error) {
	s.renewals++
	return !s.lostLease && s.lease != nil && s.lease.Token == lease.Token && s.config.Revision == lease.ConfigRevision, nil
}

func (s *ingestionTestStore) ReleaseOrderIngestion(context.Context, orders.IngestionLease) error {
	s.releases++
	s.lease = nil
	return nil
}

func (s *ingestionTestStore) CommitOrderPage(_ context.Context, page orders.SourcePage) (orders.Checkpoint, error) {
	if s.failCommit {
		return orders.Checkpoint{}, errIngestionTestCommit
	}
	if err := page.Validate(); err != nil {
		return orders.Checkpoint{}, err
	}
	s.pages = append(s.pages, page)
	checkpoint := orders.Checkpoint{Scope: page.Scope, ConfigRevision: page.ConfigRevision, Credentials: page.Credentials,
		Stream: page.Stream, RefreshKey: page.RefreshKey, Revision: page.ExpectedCheckpointRevision + 1, Cursor: page.NextCursor,
		WindowFrom: page.WindowFrom, WindowTo: page.WindowTo, Complete: page.Complete, UpdatedAt: page.FinishedAt}
	s.checkpoints[page.Stream] = checkpoint
	return checkpoint, nil
}

type ingestionTestSource struct {
	t                      *testing.T
	store                  *ingestionTestStore
	posting                orders.Posting
	pageCalls, detailCalls int
	windows                []orders.OrderWindow
	cursors                []string
	complete               bool
	onRead                 func()
}

func (s *ingestionTestSource) ReadOrderPage(_ context.Context, scope orders.Scope, window orders.OrderWindow, cursor string) (orders.OrderSourcePage, error) {
	s.t.Helper()
	if s.store.lease == nil || s.store.lease.Scope != scope {
		s.t.Fatal("HTTP began before the scope lease")
	}
	s.pageCalls++
	s.windows, s.cursors = append(s.windows, window), append(s.cursors, cursor)
	if s.onRead != nil {
		s.onRead()
	}
	page := orders.OrderSourcePage{Postings: []orders.Posting{s.posting}, Complete: s.complete}
	if !page.Complete {
		page.NextCursor = "page-next"
	}
	return page, nil
}

func (s *ingestionTestSource) ReadOrderPosting(_ context.Context, scope orders.Scope, number string) (orders.Posting, error) {
	s.t.Helper()
	if s.store.lease == nil || s.store.lease.Scope != scope {
		s.t.Fatal("detail HTTP began before scope lease")
	}
	s.detailCalls++
	posting := s.posting
	posting.PostingNumber = number
	return posting, nil
}

func newIngestionFixture(t *testing.T) (*Ingestor, *ingestionTestStore, *ingestionTestSource) {
	t.Helper()
	now := time.Now().UTC()
	scope := orders.Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}
	store := &ingestionTestStore{config: orders.Config{Scope: scope, Revision: 1, Enabled: true}, credentials: orders.CredentialRevisions{Ozon: 1, Moysklad: 1}, checkpoints: make(map[string]orders.Checkpoint)}
	posting := orders.Posting{Scope: scope, CommercialOrderID: "order-1", PostingNumber: "posting-1", Status: orders.Present("awaiting_packaging"),
		Lines:    []orders.PostingLine{{Identity: orders.LineIdentity{SKU: "sku-1", OfferID: "offer-1"}, Quantity: "1"}},
		Evidence: orders.EvidenceRevision{Revision: "fingerprint-1", Origin: orders.OriginOzon, ObservedAt: now}}
	source := &ingestionTestSource{t: t, store: store, posting: posting, complete: true}
	ingestor, err := NewIngestor(store, source, IngestionOptions{WorkerID: "worker-1", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return ingestor, store, source
}

func TestIngestionLeaseAndAtomicCheckpointResume(t *testing.T) {
	t.Parallel()
	ingestor, store, source := newIngestionFixture(t)
	source.complete = false
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, DiscoveryStream); err != nil {
		t.Fatal(err)
	}
	first := store.pages[0]
	if first.FenceToken != "lease-1" || first.Postings[0].Evidence.LocalSequence != 1 || first.Postings[0].Evidence.CollectionID != "lease-1" || !first.FreshDetail {
		t.Fatal("pre-HTTP fence evidence was replaced")
	}
	if first.Postings[0].Evidence.CollectionKind != orders.CollectionSerialDetail || first.Postings[0].Evidence.ProviderUpdatedAt.IsKnown() {
		t.Fatal("local collection was confused with provider time")
	}
	if store.checkpoints[DiscoveryStream].Cursor != "page-next" || store.renewals != 1 || store.releases != 1 {
		t.Fatal("checkpoint or lease was not finalized")
	}
	initialWindow := source.windows[0]
	previousNow := ingestor.options.Now()
	ingestor.options.Now = func() time.Time { return previousNow.Add(time.Hour) }
	source.complete = true
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, DiscoveryStream); err != nil {
		t.Fatal(err)
	}
	if source.cursors[1] != "page-next" || source.windows[1] != initialWindow {
		t.Fatal("incomplete window did not resume exactly")
	}
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, DiscoveryStream); err != nil {
		t.Fatal(err)
	}
	if source.cursors[2] != "" || store.pages[2].RefreshKey == first.RefreshKey || !source.windows[2].From.Equal(initialWindow.To.Add(-time.Hour)) {
		t.Fatal("completed traversal did not start a new overlapping refresh")
	}
}

func TestIngestionFailureDoesNotAdvanceCheckpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(*ingestionTestStore, *ingestionTestSource)
		want  error
	}{
		{"commit failure", func(store *ingestionTestStore, _ *ingestionTestSource) { store.failCommit = true }, errIngestionTestCommit},
		{"lost lease", func(store *ingestionTestStore, _ *ingestionTestSource) { store.lostLease = true }, orders.ErrStale},
		{"config changed during HTTP", func(store *ingestionTestStore, source *ingestionTestSource) {
			source.onRead = func() { store.config.Revision++ }
		}, orders.ErrStale},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ingestor, store, source := newIngestionFixture(t)
			tc.setup(store, source)
			err := ingestor.CollectStream(context.Background(), store.config.Scope, DiscoveryStream)
			if !errors.Is(err, tc.want) || len(store.pages) != 0 || store.checkpoints[DiscoveryStream].Revision != 0 || store.releases != 1 {
				t.Fatal("failed batch advanced or leaked its lease")
			}
		})
	}
}

func TestIngestionOldActiveKeysRemainRefreshable(t *testing.T) {
	t.Parallel()
	ingestor, store, source := newIngestionFixture(t)
	old := source.posting
	old.PostingNumber = "old-posting"
	old.Times.CreatedAt = orders.Present(ingestor.options.Now().Add(-100 * 24 * time.Hour))
	store.active = orders.PostingPage{Postings: []orders.Posting{old}}
	if err := ingestor.Collect(context.Background(), store.config.Scope); err != nil {
		t.Fatal(err)
	}
	if source.pageCalls != 1 || source.detailCalls != 1 || len(store.pages) != 2 {
		t.Fatal("cycle exceeded a page or skipped active refresh")
	}
	if store.pages[1].Stream != ActiveStream || store.pages[1].Postings[0].PostingNumber != "old-posting" {
		t.Fatal("active refresh was restricted by creation time")
	}
}

func TestIngestionExpiredGrantAndContentionAvoidHTTP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		expired, busy bool
	}{{"expired grant", true, false}, {"competing collector", false, true}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ingestor, store, source := newIngestionFixture(t)
			store.busy = tc.busy
			if tc.expired {
				now := ingestor.options.Now()
				store.config.Automation = &orders.AutomationGrant{ID: "grant", Scope: store.config.Scope, Revision: 1, ConfigRevision: 1, OwnerID: "owner", SourceAccount: store.config.Scope.OzonAccount, Actions: []orders.Action{orders.ActionMirrorCreate}, GrantedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
			}
			err := ingestor.CollectStream(context.Background(), store.config.Scope, DiscoveryStream)
			if tc.expired && !errors.Is(err, orders.ErrBlocked) {
				t.Fatal("expired grant was accepted")
			}
			if !tc.expired && err != nil {
				t.Fatal(err)
			}
			if source.pageCalls != 0 || source.detailCalls != 0 || len(store.pages) != 0 {
				t.Fatal("unowned batch performed provider reads")
			}
		})
	}
}

func TestIngestionCredentialRotationRestartsIncompleteCheckpoint(t *testing.T) {
	t.Parallel()
	ingestor, store, source := newIngestionFixture(t)
	now := ingestor.options.Now()
	store.checkpoints[DiscoveryStream] = orders.Checkpoint{Scope: store.config.Scope, Stream: DiscoveryStream, ConfigRevision: 1, Credentials: orders.CredentialRevisions{Ozon: 1, Moysklad: 1}, Revision: 7, Cursor: "stale-cursor", RefreshKey: "old-key", WindowFrom: now.Add(-time.Hour), WindowTo: now}
	store.credentials.Ozon = 2
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, DiscoveryStream); err != nil {
		t.Fatal(err)
	}
	page := store.pages[0]
	if store.claims != 2 || store.releases != 2 || source.cursors[0] != "" || page.RefreshKey == "old-key" || page.ExpectedCheckpointRevision != 7 || page.ExpectedCursor != "" || page.Credentials.Ozon != 2 {
		t.Fatal("credential rotation reused stale traversal or fence")
	}
}

func TestIngestionPIIRequiresProtection(t *testing.T) {
	t.Parallel()
	ingestor, store, source := newIngestionFixture(t)
	store.config.PIIEnabled = true
	source.posting.PII = &orders.Customer{Name: orders.Present("private-name")}
	err := ingestor.CollectStream(context.Background(), store.config.Scope, DiscoveryStream)
	if !errors.Is(err, orders.ErrBlocked) || len(store.pages) != 0 {
		t.Fatal("unprotected customer data reached persistence")
	}
	store.config.PIIEnabled = false
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, DiscoveryStream); err != nil {
		t.Fatal(err)
	}
	if store.pages[0].Postings[0].PII != nil {
		t.Fatal("disabled customer data was retained")
	}
}
