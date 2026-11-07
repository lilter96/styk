//go:build integration

package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestNativeStateProvenancePostgresUsesActualReconciledStateWrite(t *testing.T) {
	f := orderInventoryDBFixture(t)
	posting := orderInventoryPosting(t, f, "state-posting", "awaiting_packaging")
	correlation, err := orders.CorrelationFor(posting.Scope, posting.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	correlation.CustomerOrderID = "native-order"
	command := orders.Command{ID: "native-command", Scope: posting.Scope, Action: orders.ActionMirrorCreate, PostingNumber: posting.PostingNumber, Correlation: correlation, CustomerOrder: &orders.CustomerOrderWrite{StateID: "own-state"}}
	work := orders.Work{ID: "native-work", OperationID: "native-operation", Scope: posting.Scope, ConfigRevision: f.config.Revision, Command: command, State: orders.StateReconciled, DispatchCount: 1, CommandFingerprint: strings.Repeat("a", 64), Receipt: orders.Receipt{CommandID: command.ID, State: orders.StateAcknowledged, HTTPStatus: 200, ExternalID: correlation.CustomerOrderID, ObservedAt: f.now.Add(-time.Minute)}}
	target := orders.TargetObservation{Scope: posting.Scope, Correlation: correlation, Revision: "own-revision", Complete: true, ObservedAt: f.now, Fields: orders.CustomerOrderWrite{StateID: "own-state"}}
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	orderInventorySeedWork(t, f, tx, work, target)
	current := orders.ReviewContext{Config: f.config, Postings: []orders.Posting{posting}}
	if err = enrichOrderNativeStateBaselines(f.ctx, tx, &current, []string{posting.PostingNumber}); err != nil {
		t.Fatal(err)
	}
	baseline, found := current.NativeStateBaselines[posting.PostingNumber]
	if !found || !baseline.OwnsTarget(target) || baseline.WorkID != work.ID {
		t.Fatal("immutable exact state origin not reconstructed", baseline)
	}
	for _, foreign := range []orders.Scope{{Tenant: "foreign", MoyskladAccount: posting.Scope.MoyskladAccount, OzonAccount: posting.Scope.OzonAccount}, {Tenant: posting.Scope.Tenant, MoyskladAccount: "foreign", OzonAccount: posting.Scope.OzonAccount}, {Tenant: posting.Scope.Tenant, MoyskladAccount: posting.Scope.MoyskladAccount, OzonAccount: "foreign"}} {
		current.Config.Scope = foreign
		if err = enrichOrderNativeStateBaselines(f.ctx, tx, &current, []string{posting.PostingNumber}); err != nil || len(current.NativeStateBaselines) != 0 {
			t.Fatal("foreign scope acquired state provenance", err)
		}
	}
}
