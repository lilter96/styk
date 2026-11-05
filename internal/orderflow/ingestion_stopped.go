package orderflow

import (
	"context"
	"errors"

	"github.com/lilter96/styk/internal/orders"
)

// Only successful durable admission promotes an adapter's method-phase stop
// into this tick-local marker. No account ban, backoff clock or new queue is
// created; the next configured collection can retry under the shared budgets.
type committedOzonDetailStop struct{ cause error }

func (e *committedOzonDetailStop) Error() string {
	return "persisted partial Ozon detail phase stopped"
}
func (e *committedOzonDetailStop) Unwrap() error { return e.cause }

func (i *Ingestor) persistLeasedResult(ctx context.Context, config orders.Config, page *orders.SourcePage, lease orders.IngestionLease, readErr error) error {
	if err := i.persistLeasedPage(ctx, config, page, lease); err != nil {
		return errors.Join(err, readErr)
	}
	var stopped interface{ OzonDetailPhaseStopped() bool }
	if (page.Stream == DiscoveryStream || page.Stream == UnfulfilledStream) && errors.As(readErr, &stopped) && stopped.OzonDetailPhaseStopped() {
		return &committedOzonDetailStop{cause: readErr}
	}
	return readErr
}

func (i *Ingestor) collectActiveAfterDiscovery(ctx context.Context, scope orders.Scope, discoveryErr error) error {
	var stopped *committedOzonDetailStop
	if errors.As(discoveryErr, &stopped) {
		// Both streams would call the same stopped v3/get method. Independent native
		// MoySklad point reads are still collected by Collect after this returns.
		return nil
	}
	return i.CollectStream(ctx, scope, ActiveStream)
}
