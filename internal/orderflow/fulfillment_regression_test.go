package orderflow

import (
	"context"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

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
