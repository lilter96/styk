package stocksync

import (
	"context"
	"log/slog"
	"time"

	"github.com/lilter96/styk/internal/config"
	"github.com/lilter96/styk/internal/postgres"
)

// AutomaticRunner schedules durable imports and decisions. Marketplace reads
// after an acknowledgement are delegated to the existing reconciler; writes
// are dispatched only by Runner's normal outbox path.
type AutomaticRunner struct {
	Store     *postgres.Store
	Publisher Runner
	Log       *slog.Logger
}

func (r AutomaticRunner) RunNext(ctx context.Context) (bool, error) {
	_, simulator := config.StockPublicationTestEndpoint()
	if r.Store == nil || (!r.Publisher.ControlledTest && !simulator) || !r.Publisher.Enabled {
		return false, nil
	}
	action, worked, err := r.Store.AdvanceAutomaticStock(ctx, time.Now().UTC())
	if err != nil || action == nil {
		return worked, err
	}
	read, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err = r.Publisher.Reconcile(read, action.Scope, action.OperationID)
	if err != nil && r.Log != nil {
		r.Log.Warn("automatic stock readback did not complete", "operation_id", action.OperationID)
	}
	return true, err
}
