//go:build integration

package postgres

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrderHistoryFiltersBeforeLimitAndMatchingTargets(t *testing.T) {
	f := orderInventoryDBFixture(t)
	base := orderInventoryPosting(t, f, "base", "awaiting_packaging")
	seedPostingFilterWork(t, f, base, "RECONCILED")
	for i := range 32 {
		state := []string{"UNKNOWN", "UNKNOWN", "BLOCKED", "REJECTED"}[i%4]
		if i >= 4 {
			state = "RECONCILED"
		}
		id := fmt.Sprintf("history-%02d", i)
		seedHistoryOperation(t, f, id, f.now.Add(time.Duration(i+1)*time.Minute))
		seedHistoryWork(t, f, id, id, state)
	}
	seedHistoryOperation(t, f, "mixed", f.now.Add(time.Hour))
	seedHistoryWork(t, f, "mixed", "mixed-own", "RECONCILED")
	seedHistoryWork(t, f, "mixed", "mixed-other", "UNKNOWN")
	seedHistoryOperation(t, f, "empty", f.now.Add(2*time.Hour))
	assertHistoryFilters(t, f)
	assertHistoryPagination(t, f)
	assertHistoryEmptyAndIsolation(t, f)
}

func assertHistoryFilters(t *testing.T, f orderInventoryFixture) {
	t.Helper()
	tests := []struct {
		name string
		page orders.PageRequest
		want int
	}{
		{"status beyond first page", orders.PageRequest{Limit: 25, Status: "UNKNOWN"}, 3},
		{"attention beyond first page", orders.PageRequest{Limit: 25, Attention: true}, 5},
		{"exact posting", orders.PageRequest{Limit: 25, Search: "history-00"}, 1},
		{"exact not substring", orders.PageRequest{Limit: 25, Search: "history-0"}, 0},
		{"same work conjunction", orders.PageRequest{Limit: 25, Search: "mixed-own", Status: "UNKNOWN"}, 0},
		{"attention conjunction", orders.PageRequest{Limit: 25, Search: "mixed-own", Attention: true}, 0},
		{"combined status attention", orders.PageRequest{Limit: 25, Status: "REJECTED", Attention: true}, 1},
		{"unknown future state", orders.PageRequest{Limit: 25, Status: "FUTURE"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.db.OrderHistory(f.ctx, f.config.Scope, tc.page)
			if err != nil || len(page.Operations) != tc.want {
				t.Fatalf("history: %+v, err: %v", page, err)
			}
			assertHistoryTargets(t, page, tc.page)
		})
	}
}

func assertHistoryTargets(t *testing.T, page orders.HistoryPage, filter orders.PageRequest) {
	t.Helper()
	for _, operation := range page.Operations {
		for _, work := range operation.Targets {
			if filter.Search != "" && work.Command.PostingNumber != filter.Search {
				t.Fatalf("unmatched posting: %+v", work)
			}
			if filter.Status != "" && string(work.State) != filter.Status {
				t.Fatalf("unmatched state: %+v", work)
			}
			if filter.Attention && work.State != orders.StateUnknown && work.State != orders.StateRejected && work.State != orders.StateBlocked {
				t.Fatalf("non-attention work: %+v", work)
			}
		}
	}
}

func assertHistoryPagination(t *testing.T, f orderInventoryFixture) {
	t.Helper()
	request := orders.PageRequest{Limit: 1, Status: "UNKNOWN"}
	seen := make(map[string]bool, 3)
	for {
		page, err := f.db.OrderHistory(f.ctx, f.config.Scope, request)
		if err != nil || len(page.Operations) != 1 {
			t.Fatalf("page: %+v, err: %v", page, err)
		}
		id := page.Operations[0].ID
		if seen[id] || len(page.Operations[0].Targets) != 1 {
			t.Fatalf("duplicate or mixed work: %+v", page)
		}
		seen[id] = true
		if page.NextCursor == "" {
			break
		}
		request.Cursor = page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("lost operations: %v", seen)
	}
}

func assertHistoryEmptyAndIsolation(t *testing.T, f orderInventoryFixture) {
	t.Helper()
	page, err := f.db.OrderHistory(f.ctx, f.config.Scope, orders.PageRequest{Limit: 1})
	if err != nil || len(page.Operations) != 1 || page.Operations[0].ID != "empty" || len(page.Operations[0].Targets) != 0 || page.NextCursor == "" {
		t.Fatalf("empty operation lost: %+v, %v", page, err)
	}
	for _, field := range []string{"tenant", "moy", "ozon", "search", "status", "attention"} {
		scope := f.config.Scope
		request := orders.PageRequest{Limit: 1, Cursor: page.NextCursor}
		switch field {
		case "tenant":
			scope.Tenant = "other"
		case "moy":
			scope.MoyskladAccount = "other"
		case "ozon":
			scope.OzonAccount = "other"
		case "search":
			request.Search = "other"
		case "status":
			request.Status = "UNKNOWN"
		case "attention":
			request.Attention = true
		}
		if _, err := f.db.OrderHistory(f.ctx, scope, request); !errors.Is(err, orders.ErrInvalid) {
			t.Fatalf("cursor reused across %s: %v", field, err)
		}
		if field == "tenant" || field == "moy" || field == "ozon" {
			assertHistoryScopeEmpty(t, f, scope)
		}
	}
}

func assertHistoryScopeEmpty(t *testing.T, f orderInventoryFixture, scope orders.Scope) {
	t.Helper()
	page, err := f.db.OrderHistory(f.ctx, scope, orders.PageRequest{Limit: 25, Attention: true})
	if err != nil || len(page.Operations) != 0 {
		t.Fatalf("cross-account history leak: %+v, %v", page, err)
	}
}

func seedHistoryOperation(t *testing.T, f orderInventoryFixture, id string, at time.Time) {
	t.Helper()
	_, err := f.db.Pool.Exec(f.ctx, `INSERT INTO orders_operations(id,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,owner_user_id,automatic,request_key,request_hash,created_at)
	 SELECT $1,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,owner_user_id,automatic,$1,request_hash,$2 FROM orders_operations WHERE id='filter-operation'`, id, at)
	if err != nil {
		t.Fatal(err)
	}
}

func seedHistoryWork(t *testing.T, f orderInventoryFixture, operation, posting, state string) {
	t.Helper()
	source := orderInventoryPosting(t, f, posting, "awaiting_packaging")
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	scope := f.config.Scope
	work := orders.Work{ID: "work-" + posting, OperationID: operation, Scope: scope, ConfigRevision: f.config.Revision,
		State: orders.State(state), CommandFingerprint: strings.Repeat("a", 64),
		Command: orders.Command{ID: "command-" + posting, Scope: scope, Action: orders.ActionMirrorCreate, PostingNumber: posting}}
	_, err = tx.Exec(f.ctx, `INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,created_at,updated_at)
	 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'mirror_create',$10,$11,$12,$12)`, work.ID, operation, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, work.ConfigRevision, posting, work.Command.ID, work.CommandFingerprint, state, inventoryJSON(t, work), f.now)
	if err != nil {
		t.Fatal(err)
	}
	taskState := "failed"
	if state == "UNKNOWN" {
		taskState = "pending"
	}
	if state == "RECONCILED" {
		taskState = "succeeded"
		if _, err = tx.Exec(f.ctx, `INSERT INTO orders_reconciliations(work_id,tenant_id,moysklad_account,ozon_account,correlation,observed_posting,created_at) VALUES($1,$2,$3,$4,'{}'::jsonb,$5,$6)`, work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, inventoryJSON(t, source), f.now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO outbox_tasks(operation_key,task_type,state,orders_operation_id,orders_work_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account,max_attempts) VALUES($1,'orders_sync',$7,$2,$3,$4,$5,$6,1)`, work.ID, operation, work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, taskState); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}
