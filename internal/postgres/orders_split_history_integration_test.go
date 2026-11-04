//go:build integration

package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func historicalSplitFixture(t *testing.T) (splitReadbackTestFixture, orders.Receipt) {
	t.Helper()
	f := orderInventoryDBFixture(t)
	before := orderInventoryPosting(t, f, "history-parent", "awaiting_packaging")
	if err := ordersDecode(inventoryJSON(t, before), &before); err != nil {
		t.Fatal(err)
	}
	correlation, err := orders.CorrelationFor(f.config.Scope, before.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	lines := []orders.ActionPackage{}
	postings := []orders.SplitResponsePosting{}
	for index, number := range []string{"child-a", "child-b"} {
		quantity := orders.Decimal("1")
		if index == 1 {
			quantity = "2"
		}
		lines = append(lines, orders.ActionPackage{Lines: []orders.ActionQuantity{{Identity: before.Lines[0].Identity, Quantity: quantity}}})
		postings = append(postings, orders.SplitResponsePosting{PostingNumber: orders.Present(number), Products: orders.Present([]orders.SplitResponseProduct{{SKU: "2001", Quantity: quantity}})})
	}
	command := orders.Command{ID: "history-command", Scope: f.config.Scope, Action: orders.ActionPostingSplitUnassembled, PostingNumber: before.PostingNumber, CommercialOrderID: before.CommercialOrderID, Correlation: correlation, ExpectedSourceRevision: before.Evidence.Revision, PostingAction: &orders.PostingAction{Parameters: orders.ActionParameters{Packages: lines}}}
	ack := orders.Receipt{CommandID: command.ID, State: orders.StateAcknowledged, HTTPStatus: 200, ObservedAt: f.now.Add(-time.Second), UnassembledSplit: &orders.UnassembledSplitAck{Postings: orders.Present(postings)}}
	receipt := ack
	receipt.State = orders.StateReconciled
	receipt.PostingNumbers = []string{"child-a", "child-b"}
	work := orders.Work{ID: "history-work", OperationID: "history-operation", PreviewID: "history-preview", Scope: f.config.Scope, ConfigRevision: f.config.Revision, State: orders.StateReconciled, DispatchCount: 1, Command: command, CommandFingerprint: strings.Repeat("b", 64), Receipt: receipt}
	result := splitReadbackTestFixture{orderInventoryFixture: f, work: work, before: before}
	seedHistoricalSplitFixture(t, result, ack)
	return result, ack
}

func seedHistoricalSplitFixture(t *testing.T, f splitReadbackTestFixture, ack orders.Receipt) {
	t.Helper()
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	historicalExec(t, f, tx, `INSERT INTO orders_previews(id,tenant_id,moysklad_account,ozon_account,config_revision,preview,fingerprint,actor,automatic,owner_user_id,created_at,expires_at) SELECT $1,tenant_id,moysklad_account,ozon_account,revision,$6,$2,actor,false,owner_user_id,$3,$4 FROM orders_config_revisions WHERE tenant_id=$5`, f.work.PreviewID, strings.Repeat("b", 64), f.now.Add(-time.Minute), f.now.Add(time.Minute), f.config.Scope.Tenant, inventoryJSON(t, orders.Preview{ID: f.work.PreviewID, Scope: f.config.Scope, ConfigRevision: f.config.Revision}))
	historicalExec(t, f, tx, `INSERT INTO orders_operations(id,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,owner_user_id,automatic,request_key,request_hash,created_at) SELECT $1,tenant_id,moysklad_account,ozon_account,revision,$2,actor,owner_user_id,false,'history-request',$3,$4 FROM orders_config_revisions WHERE tenant_id=$5`, f.work.OperationID, f.work.PreviewID, strings.Repeat("b", 64), ack.ObservedAt, f.config.Scope.Tenant)
	scope := f.config.Scope
	historicalExec(t, f, tx, `INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,dispatch_count,dispatched_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'RECONCILED',$11,1,$12,$12,$12)`, f.work.ID, f.work.OperationID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, f.config.Revision, f.before.PostingNumber, f.work.Command.ID, f.work.CommandFingerprint, f.work.Command.Action, inventoryJSON(t, f.work), ack.ObservedAt)
	historicalExec(t, f, tx, `INSERT INTO outbox_tasks(operation_key,task_type,state,orders_operation_id,orders_work_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account,max_attempts) VALUES('history-outbox','orders_sync','succeeded',$1,$2,$3,$4,$5,1)`, f.work.OperationID, f.work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount)
	seedSplitReadbackDispatch(t, f, tx)
	historicalExec(t, f, tx, `INSERT INTO orders_receipts(work_id,tenant_id,moysklad_account,ozon_account,receipt,created_at) VALUES($1,$2,$3,$4,$5,$6)`, f.work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, inventoryJSON(t, ack), ack.ObservedAt)
	historicalExec(t, f, tx, `INSERT INTO orders_reconciliations(work_id,tenant_id,moysklad_account,ozon_account,correlation,created_at) VALUES($1,$2,$3,$4,$5,$6)`, f.work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, inventoryJSON(t, f.work.Command.Correlation), f.now)
	seedHistoricalSplitMembers(t, f, tx)
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func historicalExec(t *testing.T, f splitReadbackTestFixture, tx pgx.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func seedHistoricalSplitMembers(t *testing.T, f splitReadbackTestFixture, tx pgx.Tx) {
	t.Helper()
	for index, number := range f.work.Receipt.PostingNumbers {
		cell := f.observed(t, number, f.now.Add(-300*time.Millisecond))
		if index == 1 {
			cell.Posting.Lines[0].Quantity = "2"
		}
		var err error
		cell.Posting.Evidence.Revision, err = orders.PostingFingerprint(*cell.Posting)
		if err != nil {
			t.Fatal(err)
		}
		if err = saveSplitReadbackCell(f.ctx, tx, f.work, cell, 0, false, f.now); err != nil {
			t.Fatal(err)
		}
	}
	failure := orders.SplitReadback{PostingNumber: f.before.PostingNumber, Code: "SOURCE_UNAVAILABLE", CheckedAt: f.now.Add(-300 * time.Millisecond)}
	if err := saveSplitReadbackCell(f.ctx, tx, f.work, failure, 0, false, f.now); err != nil {
		t.Fatal(err)
	}
}

func TestHistoricalSplitHydratesConfirmedMembersWithoutCurrentHeadOrParentAbsenceClaim(t *testing.T) {
	f, _ := historicalSplitFixture(t)
	changed := f.observed(t, "child-a", f.now.Add(-time.Millisecond))
	changed.Posting.Status = orders.Present("delivering")
	var err error
	changed.Posting.Evidence.Revision, err = orders.PostingFingerprint(*changed.Posting)
	if err != nil {
		t.Fatal(err)
	}
	admitSplitReadbackSource(t, f, *changed.Posting)
	work := f.work
	if err = hydrateHistoricalSplitWork(f.ctx, f.db.Pool, f.config.Scope, &work); err != nil {
		t.Fatal("immutable generation lost when current child advanced", err)
	}
	if len(work.Receipt.ObservedPostings) != 2 || *work.Receipt.ObservedPostings[0].Status.Value != "awaiting_packaging" {
		t.Fatal("historical proof substituted current state or required unavailable nonmember parent")
	}
	if !orders.SplitObservedForAction(work.Command.Action, f.before, work.Command.PostingAction.Parameters.Packages, work.Receipt.PostingNumbers, work.Receipt.ObservedPostings) {
		t.Fatal("hydrated allocation not conserving")
	}
	var count int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT jsonb_array_length(COALESCE(work#>'{receipt,observed_postings}','[]'::jsonb)) FROM orders_work WHERE id=$1`, work.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("query-time hydration expanded durable work", err)
	}
}

func TestHistoricalSplitRejectsForgedWorkAckMemberSetAndScope(t *testing.T) {
	f, _ := historicalSplitFixture(t)
	for _, name := range []string{"scope", "http_status", "ack_time", "ack_contents", "members_missing", "members_reordered", "members_duplicate", "not_reconciled", "dispatch_missing", "fingerprint", "command_scope", "command_revision"} {
		t.Run(name, func(t *testing.T) {
			var work orders.Work
			if err := ordersDecode(inventoryJSON(t, f.work), &work); err != nil {
				t.Fatal(err)
			}
			scope := f.config.Scope
			switch name {
			case "scope":
				scope.OzonAccount = "other"
			case "http_status":
				work.Receipt.HTTPStatus = 201
			case "ack_time":
				work.Receipt.ObservedAt = work.Receipt.ObservedAt.Add(time.Second)
			case "ack_contents":
				work.Receipt.UnassembledSplit.Postings.Value = nil
			case "members_missing":
				work.Receipt.PostingNumbers = []string{"child-a", "forged"}
			case "members_reordered":
				work.Receipt.PostingNumbers = []string{"child-b", "child-a"}
			case "members_duplicate":
				work.Receipt.PostingNumbers = []string{"child-a", "child-a"}
			case "not_reconciled":
				work.State = orders.StatePolling
			case "dispatch_missing":
				work.DispatchCount = 0
			case "fingerprint":
				work.CommandFingerprint = strings.Repeat("f", 64)
			case "command_scope":
				work.Command.Scope.Tenant = "other"
			case "command_revision":
				work.Command.ExpectedSourceRevision = "forged-source"
			}
			if err := hydrateHistoricalSplitWork(f.ctx, f.db.Pool, scope, &work); err == nil {
				t.Fatal("forged split history was accepted")
			}
		})
	}
}

func TestHistoricalSplitRequiresEveryConfirmedMemberObservation(t *testing.T) {
	f, _ := historicalSplitFixture(t)
	historicalExecOutside(t, f, `UPDATE orders_split_readbacks SET observation_id=NULL,read_code='SOURCE_UNAVAILABLE' WHERE work_id=$1 AND posting_number='child-b'`, f.work.ID)
	work := f.work
	if err := hydrateHistoricalSplitWork(f.ctx, f.db.Pool, f.config.Scope, &work); !errors.Is(err, orders.ErrStale) {
		t.Fatal("missing confirmed member silently hydrated a partial proof", err)
	}
}

func historicalExecOutside(t *testing.T, f splitReadbackTestFixture, query string, args ...any) {
	t.Helper()
	if _, err := f.db.Pool.Exec(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}
