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

// Faults affect only the transient final preflight or an independently admitted
// read. The real merchant approval, outbox, claims and database guard remain.
type supersededReserveGuardFault struct {
	*postgres.Store
	fixture positionHTTPFixture
	test    *testing.T
	field   string
}

func (s supersededReserveGuardFault) GuardOrderDispatch(ctx context.Context, guard orders.DispatchGuard) (bool, error) {
	if !guard.Begin || guard.Work.Command.SupersededSplitWorkID == "" {
		return s.Store.GuardOrderDispatch(ctx, guard)
	}
	past := time.Now().Add(-time.Minute)
	switch s.field {
	case "source_clock":
		parent := *guard.SplitParentSource
		parent.Evidence.ObservedAt, parent.Evidence.CheckedAt = past, past
		guard.SplitParentSource = &parent
	case "native_clock":
		target := *guard.SplitSupersededTarget
		target.ObservedAt = past
		guard.SplitSupersededTarget = &target
	case "child_clock":
		guard.SplitSupersededChildren[0].Evidence.ObservedAt = past
		guard.SplitSupersededChildren[0].Evidence.CheckedAt = past
	case "partial_children":
		guard.SplitSupersededChildren = guard.SplitSupersededChildren[:1]
	case "missing_child_quantity":
		guard.SplitSupersededChildren[0].Lines[0].Quantity = ""
	case "newer_child_head":
		s.admitNewerChild(guard.SplitSupersededChildren[0])
	}
	guard.Now = past // Database expiry must never follow this caller clock.
	return s.Store.GuardOrderDispatch(ctx, guard)
}

func (s supersededReserveGuardFault) admitNewerChild(child orders.Posting) {
	s.test.Helper()
	child.Status = orders.Present("delivering")
	child.Evidence.ProviderUpdatedAt = orders.Present(time.Now().UTC().Truncate(time.Microsecond))
	child.Evidence.ObservedAt = time.Now().UTC().Truncate(time.Microsecond)
	var err error
	child.Evidence.Revision, err = orders.PostingFingerprint(child)
	if err != nil {
		s.test.Fatal("fingerprint newer independent child", err)
	}
	ordersStoreCommit(s.test, s.fixture.ordersStoreFixture, child, "superseded-newer-child-head")
	current, err := s.OrderReviewContext(s.fixture.ctx, s.fixture.config.Scope)
	if err != nil {
		s.test.Fatal(err)
	}
	for _, posting := range current.Postings {
		if posting.PostingNumber == child.PostingNumber && posting.Status.IsKnown() && *posting.Status.Value == "delivering" {
			return
		}
	}
	s.test.Fatal("independently admitted newer child did not become current")
}

func TestOrdersSupersededSplitFinalGuardRejectsUnsafeFreshnessOrChildFrontier(t *testing.T) {
	for _, field := range []string{"source_clock", "native_clock", "child_clock", "partial_children", "missing_child_quantity", "newer_child_head"} {
		t.Run(field, func(t *testing.T) {
			f, provider, split := supersededSplitHTTPFixture(t)
			f.approve(t, f.preview(t))
			store := supersededReserveGuardFault{Store: f.db, fixture: f, test: t, field: field}
			var err error
			f.runner, err = orderflow.NewRunner(store, f.factory, orderflow.RunnerOptions{WorkerID: "superseded-guard-worker", Service: f.service, PollInterval: time.Millisecond, AllowScope: func(scope orders.Scope) bool { return scope == f.config.Scope }})
			if err != nil {
				t.Fatal(err)
			}
			f.run(t, orders.StateBlocked)
			assertSupersededReserveNeverDispatched(t, f, split.ID)
			if provider.writes.Load() != 1 {
				t.Fatal("original split was replayed")
			}
		})
	}
}

func assertSupersededReserveNeverDispatched(t *testing.T, f positionHTTPFixture, splitID string) {
	t.Helper()
	work := f.latestWork(t)
	if work.State != orders.StateBlocked || work.DispatchCount != 0 || work.Command.SupersededSplitWorkID != splitID || f.provider.patches.Load() != 0 {
		t.Fatalf("unsafe retirement dispatched: state=%s count=%d patches=%d", work.State, work.DispatchCount, f.provider.patches.Load())
	}
	assertReservePolicyNative(t, f, "2")
}

func TestOrdersSupersededSplitLostReserveAcknowledgmentNeverResends(t *testing.T) {
	f, provider, _ := supersededSplitHTTPFixture(t)
	f.approve(t, f.preview(t))
	f.provider.losePatchResponse.Store(true)
	f.run(t, orders.StateUnknown)
	unknown := f.latestWork(t)
	if unknown.DispatchCount != 1 || f.provider.patches.Load() != 1 {
		t.Fatal("lost acknowledgment did not preserve exact dispatched UNKNOWN")
	}
	assertReservePolicyNative(t, f, "0")
	f.runner = f.restartedRunner(t)
	f.run(t, orders.StateReconciled)
	if f.provider.patches.Load() != 1 || provider.writes.Load() != 1 {
		t.Fatal("restart replayed reserve retirement or split")
	}
	if next := f.preview(t); !next.Decisions[0].Ready || len(next.Decisions[0].Commands) != 0 {
		t.Fatal("matching readback generated another retirement")
	}
}
