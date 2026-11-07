package orders

import (
	"testing"
	"time"
)

func nativeStateFixture(t *testing.T) (Work, TargetObservation) {
	t.Helper()
	scope := Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}
	correlation, err := CorrelationFor(scope, "posting")
	if err != nil {
		t.Fatal(err)
	}
	correlation.CustomerOrderID = "native-order"
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	command := Command{ID: "command", Scope: scope, Action: ActionMirrorCreate, Correlation: correlation, PostingNumber: "posting", CustomerOrder: &CustomerOrderWrite{StateID: "own-state"}}
	work := Work{ID: "work", Scope: scope, State: StateReconciled, DispatchCount: 1, Command: command,
		Receipt: Receipt{State: StateAcknowledged, CommandID: command.ID, HTTPStatus: 200, ExternalID: correlation.CustomerOrderID, ObservedAt: now}}
	target := TargetObservation{Scope: scope, Correlation: correlation, Fields: CustomerOrderWrite{StateID: "own-state"}, Complete: true, Revision: "revision", ObservedAt: now.Add(time.Second)}
	return work, target
}

func TestProjectionNativeStateBaselineRequiresActualOwnedWrite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*Work, *TargetObservation)
		allow  bool
	}{
		{"create", func(*Work, *TargetObservation) {}, true},
		{"explicit state update", func(w *Work, _ *TargetObservation) {
			w.Command.Action = ActionMirrorUpdate
			w.Command.CustomerOrder.OwnedFields = []string{"state"}
		}, true},
		{"metadata readback", func(w *Work, _ *TargetObservation) {
			w.Command.Action = ActionMirrorUpdate
			w.Command.CustomerOrder.OwnedFields = []string{"name"}
		}, false},
		{"no dispatch", func(w *Work, _ *TargetObservation) { w.DispatchCount = 0 }, false},
		{"unknown receipt", func(w *Work, _ *TargetObservation) { w.Receipt.State = StateUnknown }, false},
		{"ack without reconciliation", func(w *Work, _ *TargetObservation) { w.State = StateAcknowledged }, false},
		{"different native state", func(_ *Work, target *TargetObservation) { target.Fields.StateID = "merchant-state" }, false},
		{"different document", func(w *Work, _ *TargetObservation) { w.Receipt.ExternalID = "foreign-order" }, false},
		{"read before receipt", func(_ *Work, target *TargetObservation) { target.ObservedAt = target.ObservedAt.Add(-time.Minute) }, false},
		{"partial readback", func(_ *Work, target *TargetObservation) { target.Complete = false }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			work, target := nativeStateFixture(t)
			tc.change(&work, &target)
			baseline, err := ProjectionNativeStateBaseline(work, target)
			if (err == nil) != tc.allow || tc.allow && !baseline.OwnsTarget(target) {
				t.Fatalf("unexpected origin proof: %+v err=%v", baseline, err)
			}
		})
	}
}
