package orderflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestUnfulfilledCutoffAnchorsCompletedEpochAtNow(t *testing.T) {
	t.Parallel()
	ingestor, store, source := newIngestionFixture(t)
	now := ingestor.options.Now()
	for range 3 {
		if err := ingestor.CollectStream(context.Background(), store.config.Scope, UnfulfilledStream); err != nil {
			t.Fatal(err)
		}
		window := source.windows[len(source.windows)-1]
		if !window.From.Equal(now.Add(-24*24*time.Hour)) || !window.To.Equal(now.Add(7*24*time.Hour)) || window.To.Sub(window.From) != 31*24*time.Hour {
			t.Fatalf("incorrect local cutoff horizon: %+v", window)
		}
		now = now.Add(time.Hour)
		ingestor.options.Now = func() time.Time { return now }
	}
	if store.pages[0].RefreshKey == store.pages[1].RefreshKey || source.cursors[1] != "" {
		t.Fatal("completed epoch was reused")
	}
}

func TestUnfulfilledCutoffPartialResumeAndRevisionReset(t *testing.T) {
	t.Parallel()
	for _, reset := range []string{"none", "config", "credentials"} {
		t.Run(reset, func(t *testing.T) {
			t.Parallel()
			ingestor, store, source := newIngestionFixture(t)
			source.complete = false
			if err := ingestor.CollectStream(context.Background(), store.config.Scope, UnfulfilledStream); err != nil {
				t.Fatal(err)
			}
			first := source.windows[0]
			source.complete = true
			now := ingestor.options.Now().Add(time.Hour)
			ingestor.options.Now = func() time.Time { return now }
			if reset == "config" {
				store.config.Revision++
			}
			if reset == "credentials" {
				store.credentials.Ozon++
			}
			if err := ingestor.CollectStream(context.Background(), store.config.Scope, UnfulfilledStream); err != nil {
				t.Fatal(err)
			}
			if reset == "none" {
				if source.windows[1] != first || source.cursors[1] != "page-next" || store.pages[1].RefreshKey != store.pages[0].RefreshKey {
					t.Fatal("partial traversal moved")
				}
				return
			}
			next := source.windows[1]
			if !next.From.Equal(now.Add(-24*24*time.Hour)) || !next.To.Equal(now.Add(7*24*time.Hour)) || source.cursors[1] != "" {
				t.Fatal("revision reset did not anchor a fresh cutoff epoch")
			}
		})
	}
}

func TestUnfulfilledCutoffOptionsLocalBounds(t *testing.T) {
	t.Parallel()
	for _, horizon := range []time.Duration{-time.Nanosecond, 0, time.Hour, 30 * 24 * time.Hour, 30*24*time.Hour + time.Nanosecond} {
		t.Run(horizon.String(), func(t *testing.T) {
			t.Parallel()
			_, store, source := newIngestionFixture(t)
			ingestor, err := NewIngestor(store, source, IngestionOptions{WorkerID: "worker", Lookback: 48 * time.Hour, UnfulfilledLookahead: horizon})
			invalid := horizon < 0 || horizon > 30*24*time.Hour
			if invalid {
				if !errors.Is(err, orders.ErrInvalid) {
					t.Fatal("invalid local horizon accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			now := ingestor.options.Now()
			window := ingestor.anchoredWindow(UnfulfilledStream, now)
			if !window.From.Before(now) || !window.To.After(now) || window.To.Sub(window.From) > 31*24*time.Hour {
				t.Fatal("unbounded cutoff request")
			}
			if horizon == 0 && window.To.Sub(now) != 7*24*time.Hour {
				t.Fatal("seven-day local default missing")
			}
			creation := ingestor.anchoredWindow(DiscoveryStream, now)
			if !creation.To.Equal(now) || creation.To.Sub(creation.From) != 48*time.Hour {
				t.Fatal("creation policy changed")
			}
		})
	}
}

// This fake applies the cutoff predicate, not the creation-time predicate. It
// demonstrates coverage within the local horizon, not exhaustive provider coverage.
type cutoffFilteringSource struct {
	*ingestionTestSource
	cutoff time.Time
}

func (s *cutoffFilteringSource) ReadOrderPage(ctx context.Context, scope orders.Scope, window orders.OrderWindow, cursor string) (orders.OrderSourcePage, error) {
	page, err := s.ingestionTestSource.ReadOrderPage(ctx, scope, window, cursor)
	if window.Stream == UnfulfilledStream && (s.cutoff.Before(window.From) || s.cutoff.After(window.To)) {
		page.Postings = nil
	}
	if window.Stream == DiscoveryStream && s.posting.Times.CreatedAt.Value.Before(window.From) {
		page.Postings = nil
	}
	return page, err
}

func TestUnfulfilledCutoffDiscoversOldCreatedFutureDeadline(t *testing.T) {
	t.Parallel()
	ingestor, store, source := newIngestionFixture(t)
	now := ingestor.options.Now()
	source.posting.Times.CreatedAt = orders.Present(now.Add(-60 * 24 * time.Hour))
	ingestor.source = &cutoffFilteringSource{ingestionTestSource: source, cutoff: now.Add(2 * 24 * time.Hour)}
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, DiscoveryStream); err != nil {
		t.Fatal(err)
	}
	if len(store.pages[0].Postings) != 0 {
		t.Fatal("old order unexpectedly inside creation window")
	}
	if err := ingestor.CollectStream(context.Background(), store.config.Scope, UnfulfilledStream); err != nil {
		t.Fatal(err)
	}
	if len(store.pages[1].Postings) != 1 {
		t.Fatal("future assembly deadline was not discovered")
	}
}

func TestUnfulfilledCutoffDiscoveryStreamsRemainFair(t *testing.T) {
	t.Parallel()
	ingestor, store, source := newIngestionFixture(t)
	now := ingestor.options.Now()
	for range 4 {
		if err := ingestor.Collect(context.Background(), store.config.Scope); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
		ingestor.options.Now = func() time.Time { return now }
	}
	for index, window := range source.windows {
		expected := DiscoveryStream
		if index%2 == 1 {
			expected = UnfulfilledStream
		}
		if window.Stream != expected {
			t.Fatalf("stream %d: got %s want %s", index, window.Stream, expected)
		}
	}
}
