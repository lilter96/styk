package orderflow

import (
	"context"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestRunnerLaterPackingReadbackReconcilesWithoutNativeOrProviderWrites(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"delivering", "delivered", "ship_failed"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			store, source, target, runner := dispatchedFlow(t)
			work := *store.work
			work.State = orders.StateAcknowledged
			work.Command.Action = orders.ActionPostingPack
			posting := source.posting
			posting.Status = orders.Present(status)
			if status == "ship_failed" {
				posting.Status, posting.Substatus = orders.Present("awaiting_deliver"), orders.Present("ship_failed")
			}
			work.Command.PostingAction = &orders.PostingAction{Capability: flowCapability(), Parameters: orders.ActionParameters{Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{{Identity: posting.Lines[0].Identity, Quantity: posting.Lines[0].Quantity}}}}}}
			store.work = &work
			if err := runner.reconcileFulfillment(context.Background(), work, posting); err != nil {
				t.Fatal(err)
			}
			if got := store.completions[len(store.completions)-1].State == orders.StateReconciled; got != (status != "ship_failed") {
				t.Fatal("later observed state or explicit failure was misclassified")
			}
			if target.writes != 0 || source.writes != 0 {
				t.Fatal("readback caused an extra native or Ozon mutation")
			}
		})
	}
}
