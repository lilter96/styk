package orderflow

import (
	"context"
	"strconv"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

type interruptedSplitReader struct {
	*splitReaderFixture
	calls   int
	failure error
}

func (r *interruptedSplitReader) ReadOrderPosting(ctx context.Context, scope orders.Scope, number string) (orders.Posting, error) {
	r.calls++
	if r.failure != nil {
		return orders.Posting{}, r.failure
	}
	return r.splitReaderFixture.ReadOrderPosting(ctx, scope, number)
}

func TestUnassembledSplitStopsPageForProviderWideReadRestriction(t *testing.T) {
	t.Parallel()
	for _, status := range []refreshHTTPError{401, 403, 429, 500, 503} {
		t.Run(strconv.Itoa(int(status)), func(t *testing.T) {
			t.Parallel()
			store, reader, runner := normalizedSplitFixture(t)
			limited := &interruptedSplitReader{splitReaderFixture: reader, failure: status}
			if err := runner.readbackUnassembledSplit(context.Background(), *store.work, limited); err != nil {
				t.Fatal(err)
			}
			if limited.calls != 1 || len(store.reads) != 1 || store.reads[0].Code != "SOURCE_UNAVAILABLE" || store.work.State != orders.StatePolling {
				t.Fatal("provider restriction did not stop and persist partial progress")
			}
			limited.failure = nil
			for range 4 {
				if store.work.State == orders.StateReconciled {
					break
				}
				if err := runner.readbackUnassembledSplit(context.Background(), *store.work, limited); err != nil {
					t.Fatal(err)
				}
			}
			if store.work.State != orders.StateReconciled {
				t.Fatal("read restriction prevented later recovery")
			}
		})
	}
}

func TestUnassembledSplitMissingPointDoesNotStopIndependentReads(t *testing.T) {
	t.Parallel()
	store, reader, runner := normalizedSplitFixture(t)
	missing := &interruptedSplitReader{splitReaderFixture: reader, failure: refreshHTTPError(404)}
	if err := runner.readbackUnassembledSplit(context.Background(), *store.work, missing); err != nil {
		t.Fatal(err)
	}
	if missing.calls != 10 || len(store.reads) != 10 || store.work.State != orders.StatePolling {
		t.Fatal("one missing point stopped unrelated candidates or reconciled allocation")
	}
}
