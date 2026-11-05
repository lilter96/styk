//go:build integration

package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestUnassembledUnknownSplitFencesPotentialChildrenWithoutProviderParent(t *testing.T) {
	for _, state := range []orders.State{orders.StatePolling, orders.StateUnknown} {
		t.Run(string(state), func(t *testing.T) {
			f := unassembledFenceFixture(t, state)
			child := f.observed(t, "new-child", f.now.Add(-time.Millisecond))
			admitSplitReadbackSource(t, f, *child.Posting)
			independent := f.observed(t, "independent", f.now.Add(-time.Millisecond))
			independent.Posting.CommercialOrderID = "independent-commercial"
			var err error
			independent.Posting.Evidence.Revision, err = orders.PostingFingerprint(*independent.Posting)
			if err != nil {
				t.Fatal(err)
			}
			admitSplitReadbackSource(t, f, *independent.Posting)
			// A pending attempt need not have a usable immutable preview proof.
			// PostgreSQL may evaluate a lateral expression before state filters;
			// JSON null must yield no permission, not abort the blocking lookup.
			var decisionsType string
			if err := f.db.Pool.QueryRow(f.ctx, `SELECT jsonb_typeof(preview->'decisions') FROM orders_previews WHERE id='split-read-preview'`).Scan(&decisionsType); err != nil || decisionsType != "null" {
				t.Fatal("missing-proof regression precondition", err)
			}
			result := orders.ReviewContext{Config: f.config}
			dependencies, err := readOrderSplitReserveBlocks(f.ctx, f.db.Pool, &result, []string{"new-child", "independent"})
			if err != nil || !result.SplitReserveBlocked["new-child"] || dependencies["new-child"] != f.work.ID {
				t.Fatal("pending ancestor was forgotten for newly observed child", err)
			}
			if result.SplitReserveBlocked["independent"] || dependencies["independent"] != "" || result.SplitReservePermissions["new-child"].SplitWorkID != "" {
				t.Fatal("fence crossed independent order or silently granted reserve authority")
			}
			foreign := f.config.Scope
			foreign.OzonAccount = "other-account"
			result = orders.ReviewContext{Config: orders.Config{Scope: foreign}}
			if values, err := readOrderSplitReserveBlocks(f.ctx, f.db.Pool, &result, []string{"new-child"}); err != nil || len(values) != 0 {
				t.Fatal("dependency crossed destination account", err)
			}
		})
	}
}

func unassembledFenceFixture(t *testing.T, state orders.State) splitReadbackTestFixture {
	t.Helper()
	f := orderInventoryDBFixture(t)
	before := orderInventoryPosting(t, f, "split-source", "awaiting_packaging")
	binding, err := orders.CorrelationFor(f.config.Scope, before.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	command := orders.Command{ID: "split-read-command", Scope: f.config.Scope, Action: orders.ActionPostingSplitUnassembled, PostingNumber: before.PostingNumber, CommercialOrderID: before.CommercialOrderID, Correlation: binding, ExpectedSourceRevision: before.Evidence.Revision}
	work := orders.Work{ID: "split-read-work", OperationID: "split-read-operation", Scope: f.config.Scope, ConfigRevision: f.config.Revision, State: state, DispatchCount: 1, Command: command, CommandFingerprint: strings.Repeat("a", 64), Receipt: orders.Receipt{CommandID: command.ID, State: state, ObservedAt: f.now.Add(-time.Second)}}
	fixture := splitReadbackTestFixture{orderInventoryFixture: f, work: work, before: before}
	splitReadbackSeedWork(t, fixture)
	return fixture
}
