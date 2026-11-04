package orderinventory

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
)

func TestRetainedSplitReserveTransferNeverPublishesReleasedGap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*Input)
	}{
		{"child not mirrored", func(i *Input) { i.Observations[1].Work, i.Observations[1].Target = nil, nil }},
		{"child write unknown", func(i *Input) { i.Observations[1].Work.State = orders.StateUnknown }},
		{"child reserve not applied", func(i *Input) { setReserve(&i.Observations[1], "0") }},
		{"child reserve partly applied", func(i *Input) { setReserve(&i.Observations[1], "1") }},
		{"old native register", func(i *Input) { i.Observations[1].NativeStartedAt = i.Now.Add(-6 * time.Minute) }},
		{"old source baseline", func(i *Input) { i.BaselineStartedAt = i.Now.Add(-4 * time.Minute) }},
		{"aggregate reserve not caught up", func(i *Input) { i.Baseline.Reserve = ptr("1") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := retainedTransferFixture()
			input.Baseline.Reserve = ptr("3")
			tc.edit(&input)
			decision := inventoryDecision(input)
			if decision.Status != domain.StockBlocked || decision.Quantity != nil {
				t.Fatalf("fresh aggregate reserve falsely settled incomplete child transfer: %+v", decision)
			}
			if *input.Baseline.Physical != "10" {
				t.Fatal("attribution changed authoritative source arithmetic")
			}
		})
	}
}

func TestRetainedSplitReserveTransferSettlesOnlyOnceInFreshBaseline(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ reserve, quantity string }{{"3", "7"}, {"5", "5"}} {
		t.Run("aggregate_reserve_"+tc.reserve, func(t *testing.T) {
			t.Parallel()
			input := retainedTransferFixture()
			input.Baseline.Reserve = ptr(tc.reserve)
			guard, decision := Assess(input), inventoryDecision(input)
			if guard.Blocked || len(guard.Obligations) != 2 || decision.Status != domain.StockReady || decision.Quantity == nil || *decision.Quantity != tc.quantity {
				t.Fatalf("split reserve counted twice or foreign reserve released: guard=%+v decision=%+v", guard, decision)
			}
		})
	}
}

func retainedTransferFixture() Input {
	input := inventoryFixture()
	parent := inventoryObservation(input.Now, "retained-parent", "1")
	parent.Work.Command.Action = orders.ActionMirrorPositionUpdate
	child := inventoryObservation(input.Now, "new-child", "2")
	// The provider may omit lineage. Local split authorization is enforced by
	// the projection guard, while every observed posting remains an obligation.
	input.Observations = []Observation{parent, child}
	return input
}
