//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

// The normal runner, outbox and provider still execute. Only the transient
// preflight envelope is fault-injected before the real database Begin guard.
type agedSplitTransferGuard struct {
	*postgres.Store
	field string
}

func (s agedSplitTransferGuard) GuardOrderDispatch(ctx context.Context, guard orders.DispatchGuard) (bool, error) {
	if guard.Begin && guard.Work.Command.SplitTransfer != nil {
		past := time.Now().Add(-time.Minute)
		guard.Now = past // Caller clocks must not override the database clock.
		if s.field == "source" && guard.SplitParentSource != nil {
			source := *guard.SplitParentSource
			source.Evidence.CheckedAt, source.Evidence.ObservedAt = past, past
			guard.SplitParentSource = &source
		}
		if s.field == "target" && guard.SplitParentTarget != nil {
			target := *guard.SplitParentTarget
			target.ObservedAt = past
			guard.SplitParentTarget = &target
		}
	}
	return s.Store.GuardOrderDispatch(ctx, guard)
}

func TestOrdersSplitTransferUsesDatabaseClockForFinalParentChecks(t *testing.T) {
	for _, field := range []string{"source", "target"} {
		t.Run(field, func(t *testing.T) {
			f, _, split := retainedSplitParentFixture(t)
			reduceRetainedSplitParent(t, f, split, false)
			f.approve(t, splitMemberHTTPPreview(t, f, "split-http-child", nil))
			var err error
			f.runner, err = orderflow.NewRunner(agedSplitTransferGuard{Store: f.db, field: field}, f.factory, orderflow.RunnerOptions{WorkerID: "split-parent-clock-worker", Service: f.service, PollInterval: time.Millisecond, AllowScope: func(scope orders.Scope) bool { return scope == f.config.Scope }})
			if err != nil {
				t.Fatal(err)
			}
			f.run(t, orders.StateBlocked)
			assertSplitTransferNeverDispatched(t, f)
		})
	}
}
