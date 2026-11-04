package orderflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

var errIngestionReadAuthority = errors.New("order read authority could not be verified")

// Each actual network attempt rechecks the exact durable lease after shared
// admission waiting. A revoked permission interrupts the entire leased read;
// ordinary provider errors never cancel its independent target processing.
func (i *Ingestor) ingestionReadAuthority(ctx context.Context, lease orders.IngestionLease) (context.Context, context.CancelFunc) {
	guarded, cancel := context.WithCancelCause(ctx)
	guarded = providercall.WithGuard(guarded, func(attempt context.Context) error {
		if err := i.renewLease(attempt, lease); err != nil {
			cancel(ingestionAuthorityCause(err))
			return fmt.Errorf("order read authority ended before dispatch: %w", context.Canceled)
		}
		return nil
	})
	return guarded, func() { cancel(nil) }
}

func ingestionAuthorityCause(err error) error {
	for _, safe := range []error{orders.ErrBlocked, orders.ErrStale, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return errIngestionReadAuthority
}
