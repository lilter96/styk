//go:build integration

package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func TestUnassembledCompletionRequiresNormalizedConservingFacts(t *testing.T) {
	f := splitReadbackFixture(t)
	f.before.Lines = append([]orders.PostingLine(nil), f.before.Lines...)
	f.before.Lines[0].Quantity = "2"
	if err := ordersDecode(inventoryJSON(t, f.before), &f.before); err != nil {
		t.Fatal(err)
	}
	quantity := orders.ActionQuantity{Identity: f.before.Lines[0].Identity, Quantity: "1"}
	f.work.Command.PostingAction = &orders.PostingAction{Parameters: orders.ActionParameters{Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{quantity}}, {Lines: []orders.ActionQuantity{quantity}}}}}
	completion := orders.Completion{State: orders.StateReconciled, Receipt: f.work.Receipt}
	completion.Receipt.State = orders.StateReconciled
	completion.Receipt.PostingNumbers = []string{"child-a", "child-b"}
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	if err = validateUnassembledSplitResolution(f.ctx, tx, f.before, f.work, completion); !errors.Is(err, orders.ErrStale) {
		t.Fatal("client list alone reconciled", err)
	}
	reads := []orders.SplitReadback{f.observed(t, "child-a", f.now.Add(-300*time.Millisecond)), f.observed(t, "child-b", f.now.Add(-300*time.Millisecond)), {PostingNumber: f.before.PostingNumber, Code: "SOURCE_UNAVAILABLE", CheckedAt: f.now.Add(-300 * time.Millisecond)}}
	now, err := ordersClock(f.ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveUnassembledSplitReadbacks(f.ctx, tx, f.work, orders.Completion{SplitReads: reads}, now); err != nil {
		t.Fatal(err)
	}
	if err = validateUnassembledSplitResolution(f.ctx, tx, f.before, f.work, completion); err != nil {
		t.Fatal("same-tx normalized exact allocation rejected", err)
	}
	assertUnassembledCompletionRejectsChangedEvidence(t, f, tx, completion, now)
}

func TestUnassembledCompletionPersistsPointReadBeforePendingPhase(t *testing.T) {
	f := splitReadbackFixture(t)
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	completion := orders.Completion{ExpectedState: f.work.State, State: orders.StatePolling, Receipt: f.work.Receipt, SplitReads: []orders.SplitReadback{f.observed(t, "child-a", f.now.Add(-time.Millisecond))}}
	if err = completeOrderLocked(f.ctx, tx, f.work, completion); err != nil {
		t.Fatal(err)
	}
	rows, err := readOrderSplitReadbacks(f.ctx, tx, f.work.Scope, f.work.ID)
	if err != nil || len(rows) != 1 || rows[0].Code != "OBSERVED" {
		t.Fatal("normal completion lost incremental point evidence", err)
	}
	var count int
	if err = tx.QueryRow(f.ctx, `SELECT count(*) FROM orders_reconciliations WHERE work_id=$1`, f.work.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial point read marked reconciled", err)
	}
}

func assertUnassembledCompletionRejectsChangedEvidence(t *testing.T, f splitReadbackTestFixture, tx pgx.Tx, completion orders.Completion, now time.Time) {
	t.Helper()
	var err error
	completion.Receipt.PostingNumbers = []string{"child-b", "child-a"}
	if err = validateUnassembledSplitResolution(f.ctx, tx, f.before, f.work, completion); !errors.Is(err, orders.ErrStale) {
		t.Fatal("noncanonical list accepted", err)
	}
	completion.Receipt.PostingNumbers = []string{"child-a", "forged"}
	if err = validateUnassembledSplitResolution(f.ctx, tx, f.before, f.work, completion); !errors.Is(err, orders.ErrStale) {
		t.Fatal("forged resolved identity accepted", err)
	}
	completion.Receipt.PostingNumbers = []string{"child-a", "child-b"}
	failed := orders.SplitReadback{PostingNumber: "child-a", Code: "SOURCE_UNAVAILABLE", CheckedAt: f.now.Add(-100 * time.Millisecond)}
	if err = saveUnassembledSplitReadbacks(f.ctx, tx, f.work, orders.Completion{SplitReads: []orders.SplitReadback{failed}}, now); err != nil {
		t.Fatal(err)
	}
	if err = validateUnassembledSplitResolution(f.ctx, tx, f.before, f.work, completion); !errors.Is(err, orders.ErrStale) {
		t.Fatal("new failed child read reused older success", err)
	}
}
