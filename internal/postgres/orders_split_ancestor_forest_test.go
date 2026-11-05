//go:build integration

package postgres

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

type forestVisibilityCase struct {
	name, proof string
	action      orders.Action
	state       orders.State
	dispatched  int
	otherSelf   bool
	visible     bool
	anchor      bool
}

// Exercise the actual production query against constrained durable work, not a
// rewritten predicate. Missing JSON proof must not turn NOT(NULL) into absence.
func TestOwnedForestSelfExclusionPreservesUnknownOrMissingProof(t *testing.T) {
	f := orderInventoryDBFixture(t)
	initial := orderInventoryPosting(t, f, "forest-original", "awaiting_packaging")
	orderInventoryReconciled(t, f, initial, "1")
	cases := []forestVisibilityCase{
		{"missing", "missing", orders.ActionMirrorPositionUpdate, orders.StateQueued, 0, false, true, true},
		{"null", "null", orders.ActionMirrorPositionUpdate, orders.StateQueued, 0, false, true, true},
		{"matching_reduction", "matching", orders.ActionMirrorPositionUpdate, orders.StateQueued, 0, false, false, true},
		{"matching_composition", "matching", orders.ActionMirrorCompositionUpdate, orders.StateQueued, 0, false, false, true},
		{"missing_composition", "missing", orders.ActionMirrorCompositionUpdate, orders.StateQueued, 0, false, true, true},
		{"matching_admission", "matching", orders.ActionMirrorCreate, orders.StateClaimed, 0, false, false, true},
		{"other_pending", "matching", orders.ActionMirrorPositionUpdate, orders.StateQueued, 0, true, true, true},
		{"already_dispatched", "matching", orders.ActionMirrorPositionUpdate, orders.StatePolling, 1, false, true, true},
		{"unknown", "matching", orders.ActionMirrorPositionUpdate, orders.StateUnknown, 1, false, true, true},
		{"legacy_unsent_create", "source", orders.ActionMirrorCreate, orders.StateQueued, 0, false, true, false},
		{"legacy_null_source", "null_source", orders.ActionMirrorCreate, orders.StateQueued, 0, false, true, true},
		{"node_admission", "node", orders.ActionMirrorCreate, orders.StateClaimed, 0, false, false, true},
		{"node_missing", "missing", orders.ActionMirrorCreate, orders.StateQueued, 0, false, true, true},
		{"node_null", "null", orders.ActionMirrorCreate, orders.StateQueued, 0, false, true, true},
		{"node_other_pending", "node", orders.ActionMirrorCreate, orders.StateQueued, 0, true, true, true},
		{"node_unknown", "node", orders.ActionMirrorCreate, orders.StateUnknown, 1, false, true, true},
		{"node_wrong_source", "node_wrong_source", orders.ActionMirrorCreate, orders.StateQueued, 0, false, true, true},
		{"node_wrong_node", "node_wrong_node", orders.ActionMirrorCreate, orders.StateQueued, 0, false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertForestHistoryVisibility(t, f, tc) })
	}
}

func assertForestHistoryVisibility(t *testing.T, f orderInventoryFixture, tc forestVisibilityCase) {
	t.Helper()
	posting := orderInventoryPosting(t, f, "forest-"+tc.name, "awaiting_packaging")
	work := seedForestVisibilityWork(t, f, posting, tc)
	self := work.ID
	if tc.otherSelf {
		self = "different-current-work"
	}
	args := append(ordersScopeArgs(f.config.Scope), []string{posting.PostingNumber}, self, work.CommandFingerprint, posting.PostingNumber, posting.PostingNumber, "forest-split", false, 3)
	rows, err := f.db.Pool.Query(f.ctx, ownedForestNativeSQL, args...)
	if err != nil {
		t.Fatal("query native forest history", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err = rows.Err(); err != nil || (count == 1) != tc.visible || count > 1 {
		t.Fatalf("forest history visible=%t count=%d: %v", tc.visible, count, err)
	}
	assertForestAnchorVisibility(t, f, work, self, tc.anchor)
	assertRetainedRootNativeVisibility(t, f, work, self, tc)
	assertForestNativeHistoryWitness(t, f, work, self, tc.visible)
}

func assertForestNativeHistoryWitness(t *testing.T, f orderInventoryFixture, work orders.Work, self string, visible bool) {
	t.Helper()
	number := work.Command.PostingNumber
	history, err := readOwnedForestNativeHistory(f.ctx, f.db.Pool, work.Scope, []string{number}, self, work.CommandFingerprint, number, number, "forest-split")
	if err != nil || len(history) != 1 || history[0].Scope != work.Scope || history[0].PostingNumber != number || history[0].BindingIDs == nil || len(history[0].BindingIDs) != 0 || history[0].MirrorWorkIDs == nil {
		t.Fatal("explicit forest absence witness missing or unscoped", err)
	}
	if (len(history[0].MirrorWorkIDs) == 1) != visible {
		t.Fatal("forest absence witness disagrees with complete native traversal")
	}
}

func assertRetainedRootNativeVisibility(t *testing.T, f orderInventoryFixture, work orders.Work, self string, tc forestVisibilityCase) {
	t.Helper()
	history, err := readRetainedRootNativeHistory(f.ctx, f.db.Pool, work.Scope, work.Command.PostingNumber, "forest-split", self, work.CommandFingerprint)
	if err != nil || len(history) != 1 || history[0].BindingIDs == nil || history[0].MirrorWorkIDs == nil {
		t.Fatal("actual retained-root native history traversal failed", err)
	}
	// A source marker on this exact unsent create is required. Unknown, absent,
	// null, other-target and already dispatched proof never become absence.
	absent := tc.proof == "source" && !tc.otherSelf && tc.action == orders.ActionMirrorCreate && tc.dispatched == 0 && (tc.state == orders.StateQueued || tc.state == orders.StateClaimed)
	if (len(history[0].MirrorWorkIDs) == 0) != absent {
		t.Fatal("retained-root self exception hid unproved or dispatched work")
	}
}

func assertForestAnchorVisibility(t *testing.T, f orderInventoryFixture, work orders.Work, self string, expected bool) {
	t.Helper()
	args := append(ordersScopeArgs(work.Scope), []string{work.Command.PostingNumber}, self, work.CommandFingerprint, work.Command.PostingNumber, "forest-split")
	var exists bool
	if err := f.db.Pool.QueryRow(f.ctx, ownedForestAnchorSQL, args...).Scan(&exists); err != nil || exists != expected {
		t.Fatal("actual native anchor visibility", exists, expected, err)
	}
}

func seedForestVisibilityWork(t *testing.T, f orderInventoryFixture, posting orders.Posting, tc forestVisibilityCase) orders.Work {
	t.Helper()
	command := forestVisibilityCommand(t, posting, tc)
	work := orders.Work{ID: "work-" + tc.name, OperationID: "native-operation", Scope: posting.Scope, ConfigRevision: f.config.Revision, State: tc.state, DispatchCount: tc.dispatched, Command: command, CommandFingerprint: strings.Repeat("b", 64), Receipt: orders.Receipt{ObservedAt: f.now}}
	raw := forestVisibilityJSON(t, work, tc)
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	var dispatched any
	if tc.dispatched == 1 {
		dispatched = f.now
	}
	_, err = tx.Exec(f.ctx, `INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,dispatch_count,dispatched_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15)`, work.ID, work.OperationID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, work.ConfigRevision, posting.PostingNumber, command.ID, work.CommandFingerprint, command.Action, work.State, raw, tc.dispatched, dispatched, f.now)
	if err != nil {
		t.Fatal("insert constrained forest work", err)
	}
	seedForestVisibilityOutbox(t, f, tx, work)
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal("commit constrained forest work", err)
	}
	return work
}

func forestVisibilityCommand(t *testing.T, posting orders.Posting, tc forestVisibilityCase) orders.Command {
	t.Helper()
	correlation, err := orders.CorrelationFor(posting.Scope, posting.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	command := orders.Command{ID: "command-" + tc.name, Scope: posting.Scope, Action: tc.action, PostingNumber: posting.PostingNumber, CommercialOrderID: posting.CommercialOrderID, Correlation: correlation, ExpectedSourceRevision: posting.Evidence.Revision}
	if tc.proof == "source" {
		command.SplitSourceWorkID = "forest-split"
	}
	if tc.proof == "matching" {
		mode := orders.SplitAncestorReduce
		if tc.action == orders.ActionMirrorCreate {
			mode = orders.SplitAncestorAdmit
		}
		command.SplitAncestorSettlement = &orders.SplitAncestorSettlementPermission{Mode: mode, SplitWorkID: "forest-split"}
	}
	if strings.HasPrefix(tc.proof, "node") {
		proof := &orders.SplitAncestorSettlementPermission{Mode: orders.SplitAncestorNodeAdmit, SplitWorkID: "forest-split", Source: orders.Posting{PostingNumber: posting.PostingNumber}, Node: orders.Posting{PostingNumber: posting.PostingNumber}}
		if tc.proof == "node_wrong_source" {
			proof.Source.PostingNumber = "different-source"
		}
		if tc.proof == "node_wrong_node" {
			proof.Node.PostingNumber = "different-node"
		}
		command.SplitAncestorSettlement = proof
	}
	return command
}

func forestVisibilityJSON(t *testing.T, work orders.Work, tc forestVisibilityCase) []byte {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(inventoryJSON(t, work), &raw); err != nil {
		t.Fatal(err)
	}
	command := raw["command"].(map[string]any)
	if tc.proof == "null" {
		command["split_ancestor_settlement"] = nil
	}
	if tc.proof == "null_source" {
		command["split_source_work_id"] = nil
	}
	return inventoryJSON(t, raw)
}

func seedForestVisibilityOutbox(t *testing.T, f orderInventoryFixture, tx pgx.Tx, work orders.Work) {
	t.Helper()
	_, err := tx.Exec(f.ctx, `INSERT INTO outbox_tasks(operation_key,task_type,state,orders_operation_id,orders_work_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account,max_attempts) VALUES($1,'orders_sync','pending',$2,$3,$4,$5,$6,1)`, work.ID+"-outbox", work.OperationID, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount)
	if err != nil {
		t.Fatal("insert forest work outbox", err)
	}
	if work.DispatchCount == 1 {
		seedSplitReadbackDispatch(t, splitReadbackTestFixture{orderInventoryFixture: f, work: work}, tx)
	}
}
