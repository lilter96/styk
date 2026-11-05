//go:build integration

package integration

import (
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

// These are generated local provider fixtures. Seller split acknowledgements
// remain real HTTP/outbox/worker observations; no intermediate native source
// state is fabricated or written directly to PostgreSQL.
func deferredRetainedSplits(t *testing.T, f positionHTTPFixture, quantities ...orders.Decimal) {
	t.Helper()
	for _, quantity := range quantities {
		t.Logf("deferred retained split: root retains %s before native settlement", quantity)
		nestedSettlementSplit(t, f, nestedRoot, quantity)
	}
	// Refresh current source evidence through the normal reviewed path.
	_ = unmirroredUnassembledHTTPReview(t, f, nestedRoot)
}

func deferredRetainedChildrenHeld(t *testing.T, f positionHTTPFixture, children []string) {
	t.Helper()
	for _, child := range children {
		repeatedRetainedHeld(t, f, child)
	}
}

func deferredRetainedReduce(t *testing.T, f positionHTTPFixture, root orders.TargetObservation, baseline string, children []string) {
	t.Helper()
	deferredRetainedChildrenHeld(t, f, children)
	preview := splitMemberHTTPPreview(t, f, nestedRoot, nil)
	command := preview.Decisions[0].Commands[0]
	patch := command.PositionPatch
	if command.Action != orders.ActionMirrorPositionUpdate || patch == nil || patch.Baseline.WorkID != baseline || patch.PositionID != root.Positions[0].ID || command.Correlation.CustomerOrderID != root.Correlation.CustomerOrderID || !patch.Quantity.IsKnown() || *patch.Quantity.Value != "1" || !patch.Reserve.IsKnown() || *patch.Reserve.Value != "1" {
		t.Fatal("deferred settlement did not reduce the exact latest native frontier directly to one")
	}
	f.approve(t, preview)
	f.run(t, orders.StateAcknowledged)
	deferredRetainedChildrenHeld(t, f, children)
	f.run(t, orders.StateReconciled)
	current := f.readNative(t)
	if current.Correlation.CustomerOrderID != root.Correlation.CustomerOrderID {
		t.Fatal("deferred settlement recreated the native root")
	}
	assertRepeatedRetainedPosition(t, current, root.Positions[0].ID, "1")
}

func deferredRetainedAdmit(t *testing.T, f positionHTTPFixture, children []string) {
	t.Helper()
	f.runner = f.restartedRunner(t)
	for _, child := range children {
		t.Logf("admit historical split allocation after latest retained readback: %s", child)
		deepOwnedMirror(t, f, child)
	}
	assertDeepOwnedConserved(t, f)
	for _, number := range append([]string{nestedRoot}, children...) {
		if next := splitMemberHTTPPreview(t, f, number, nil); len(next.Decisions[0].Commands) != 0 {
			t.Fatal("unchanged deferred settlement created a duplicate command")
		}
	}
}

func assertDeferredRetainedNativeRoot(t *testing.T, f positionHTTPFixture, root orders.TargetObservation, quantity orders.Decimal) {
	t.Helper()
	current := f.readNative(t)
	if current.Correlation.CustomerOrderID != root.Correlation.CustomerOrderID {
		t.Fatal("seller split changed native root identity before settlement")
	}
	assertRepeatedRetainedPosition(t, current, root.Positions[0].ID, quantity)
}

func TestOrdersDeferredRetainedSplitsSettleOnceBeforeOlderChildren(t *testing.T) {
	f, provider, root, origin := repeatedRetainedFixture(t)
	deferredRetainedSplits(t, f, "4", "2", "1")
	assertDeferredRetainedNativeRoot(t, f, root, "8")
	if f.provider.patches.Load() != 0 {
		t.Fatal("seller splits silently issued intermediate native reductions")
	}
	children := []string{nestedChild, repeatedRetainedSecond, repeatedRetainedThird}
	deferredRetainedReduce(t, f, root, origin, children)
	deferredRetainedAdmit(t, f, children)
	if provider.attempts.Load() != 3 || provider.writes.Load() != 3 || f.provider.patches.Load() != 1 {
		t.Fatal("deferred settlement replayed a split or wrote artificial native frontiers")
	}
}

func TestOrdersDeferredRetainedSplitsContinueFromSettledPrefix(t *testing.T) {
	f, provider, root, origin := repeatedRetainedFixture(t)
	nestedSettlementSplit(t, f, nestedRoot, "4")
	_, _, prefix := repeatedRetainedReduce(t, f, nestedChild, root, origin)
	deepOwnedMirror(t, f, nestedChild)
	deferredRetainedSplits(t, f, "2", "1")
	assertDeferredRetainedNativeRoot(t, f, root, "4")
	if f.provider.patches.Load() != 1 {
		t.Fatal("later seller splits invented an intermediate native frontier")
	}
	children := []string{repeatedRetainedSecond, repeatedRetainedThird}
	deferredRetainedReduce(t, f, root, prefix, children)
	deferredRetainedAdmit(t, f, children)
	if provider.attempts.Load() != 3 || provider.writes.Load() != 3 || f.provider.patches.Load() != 2 {
		t.Fatal("settled prefix was replayed or replaced with stale native authority")
	}
}

func TestOrdersDeferredRetainedUnknownCannotReleaseAnyHistoricalChild(t *testing.T) {
	f, provider, root, origin := repeatedRetainedFixture(t)
	deferredRetainedSplits(t, f, "4", "2", "1")
	children := []string{nestedChild, repeatedRetainedSecond, repeatedRetainedThird}
	deferredRetainedChildrenHeld(t, f, children)
	preview := splitMemberHTTPPreview(t, f, nestedRoot, nil)
	if command := preview.Decisions[0].Commands[0]; command.PositionPatch == nil || command.PositionPatch.Baseline.WorkID != origin || command.PositionPatch.PositionID != root.Positions[0].ID {
		t.Fatal("unknown-path review reused an intermediate native baseline")
	}
	f.approve(t, preview)
	f.provider.losePatchResponse.Store(true)
	f.run(t, orders.StateUnknown)
	unknown := f.latestWork(t)
	deferredRetainedChildrenHeld(t, f, append([]string{nestedRoot}, children...))
	repeatedRetainedIndependentReady(t, f)
	if latest := f.latestWork(t); latest.ID != unknown.ID || latest.State != orders.StateUnknown || latest.DispatchCount != 1 || provider.attempts.Load() != 3 || provider.writes.Load() != 3 || f.provider.patches.Load() != 1 {
		t.Fatal("uncertain coalesced reduction was retried or rewritten as known")
	}
	f.provider.base.mu.Lock()
	defer f.provider.base.mu.Unlock()
	if len(f.provider.base.native) != 1 {
		t.Fatal("unknown native root allowed a historical child order to be created")
	}
}
