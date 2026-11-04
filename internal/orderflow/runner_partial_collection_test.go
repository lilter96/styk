package orderflow

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestPartialCollectionDoesNotBlockIndependentFreshPosting(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"stale observation", "unknown write"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			review := flowReview(t, "failed-posting")
			review.Postings = append(review.Postings, flowReview(t, "fresh-posting").Postings[0])
			if name == "stale observation" {
				review.Postings[0].Evidence.CheckedAt = flowTestNow.Add(-time.Hour)
			} else {
				review.ActivePostings = map[string]orders.State{"failed-posting": orders.StateUnknown}
			}
			attachFlowGrant(&review)
			store := &flowStore{review: review, due: []orders.Config{review.Config}}
			runner := flowRunner(t, store, flowResolver{}, flowService(t, store, flowResolver{}))
			runner.options.Collector = &flowCollector{fail: review.Config.Scope}
			if err := runner.Tick(context.Background()); !errors.Is(err, orders.ErrStale) {
				t.Fatalf("partial traversal error lost: %v", err)
			}
			if !store.approval.Automatic || !slices.Equal(store.approval.PostingNumbers, []string{"fresh-posting"}) {
				t.Fatal("fresh independent posting did not progress, or stale/unknown posting was approved")
			}
		})
	}
}

func TestPartialCollectionWithoutGrantCannotCreateWork(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	store := &flowStore{review: review, due: []orders.Config{review.Config}}
	runner := flowRunner(t, store, flowResolver{}, flowService(t, store, flowResolver{}))
	runner.options.Collector = &flowCollector{fail: review.Config.Scope}
	if err := runner.Tick(context.Background()); !errors.Is(err, orders.ErrStale) {
		t.Fatal("read failure hidden")
	}
	if len(store.previews) != 0 || store.approval.PreviewID != "" {
		t.Fatal("missing grant created publication")
	}
}
