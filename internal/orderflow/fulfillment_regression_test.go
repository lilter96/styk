package orderflow

import (
	"context"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

type actionInputFixture struct{ input orders.ActionParameters }

func (f actionInputFixture) ReadOrderActionInput(context.Context, orders.Scope, string, orders.Action, orders.ProtectedActionInput) (orders.ActionParameters, error) {
	return f.input, nil
}

func TestTrackingReadbackRequiresExactProtectedValue(t *testing.T) {
	t.Parallel()
	store, source, _, runner := dispatchedFlow(t)
	input := orders.ActionParameters{TrackingNumber: "actual-tracking"}
	runner.options.ActionReader = actionInputFixture{input: input}
	command := store.work.Command
	command.Action = orders.ActionPostingTracking
	command.PostingAction = &orders.PostingAction{Parameters: input.Public(), ProtectedInput: &orders.ProtectedActionInput{Reference: "sealed", Revision: command.ConfigRevision}}
	posting := source.posting
	posting.Delivery.TrackingNumber = orders.Present("different-tracking")
	if runner.trackingConfirmed(context.Background(), command, posting) {
		t.Fatal("wrong tracking reconciled")
	}
	posting.Delivery.TrackingNumber = orders.Present("actual-tracking")
	if !runner.trackingConfirmed(context.Background(), command, posting) {
		t.Fatal("exact readback did not reconcile")
	}
	command.ConfigRevision++
	if runner.trackingConfirmed(context.Background(), command, posting) {
		t.Fatal("stale secret revision reconciled")
	}
}

func TestFulfillmentWithoutActionEvidenceCannotReconcile(t *testing.T) {
	t.Parallel()
	for _, action := range []orders.Action{orders.ActionPostingTracking, orders.ActionPostingPartialCancel, orders.ActionPostingSentBySeller} {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()
			store, source, target, runner := dispatchedFlow(t)
			store.work.State = orders.StateAcknowledged
			store.work.Command.Action = action
			if err := runner.reconcileFulfillment(context.Background(), *store.work, source.posting); err != nil {
				t.Fatal(err)
			}
			if store.work.State == orders.StateReconciled || target.writes != 0 {
				t.Fatal("missing evidence reconciled or caused an unjournaled write")
			}
		})
	}
}
