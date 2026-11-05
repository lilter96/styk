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

func TestOrderPostingFiltersBeforeCursorLimit(t *testing.T) {
	fixture := orderInventoryDBFixture(t)
	for i := range 30 {
		status := "awaiting_packaging"
		if i >= 25 {
			status = "delivered"
		}
		orderInventoryPosting(t, fixture, fmt.Sprintf("posting-%02d", i), status)
	}
	cases := []struct {
		name  string
		page  orders.PageRequest
		count int
		first string
	}{
		{"status beyond first page", orders.PageRequest{Limit: 2, Status: "delivered"}, 2, "posting-25"},
		{"combined status search", orders.PageRequest{Limit: 25, Status: "delivered", Search: "posting-29"}, 1, "posting-29"},
		{"unknown status", orders.PageRequest{Limit: 25, Status: "new-provider-status"}, 0, ""},
		{"unlinked is not an error", orders.PageRequest{Limit: 25, Attention: true}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, tc.page)
			if err != nil || len(page.Postings) != tc.count {
				t.Fatalf("filtered page: %+v %v", page, err)
			}
			if tc.count > 0 && page.Postings[0].PostingNumber != tc.first {
				t.Fatalf("wrong posting: %+v", page)
			}
		})
	}
	assertFilteredPostingPagination(t, fixture)
}

func assertFilteredPostingPagination(t *testing.T, fixture orderInventoryFixture) {
	t.Helper()
	request := orders.PageRequest{Limit: 2, Status: "delivered"}
	first, err := fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, request)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	request.Cursor = first.NextCursor
	next, err := fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, request)
	if err != nil || len(next.Postings) != 2 || next.Postings[0].PostingNumber != "posting-27" {
		t.Fatalf("next page: %+v %v", next, err)
	}
	request.Attention = true
	if _, err = fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, request); !errors.Is(err, orders.ErrInvalid) {
		t.Fatalf("cross-filter cursor: %v", err)
	}
	scope := fixture.config.Scope
	scope.OzonAccount = "other"
	page, err := fixture.db.OrderPostings(fixture.ctx, scope, orders.PageRequest{Limit: 25, Status: "delivered"})
	if err != nil || len(page.Postings) != 0 {
		t.Fatalf("cross-account leak: %+v %v", page, err)
	}
}

func TestOrderPostingAttentionAndOrderNumberSearch(t *testing.T) {
	cases := []struct {
		name, state string
		changed     bool
		count       int
	}{
		{"reconciled", "RECONCILED", false, 0}, {"blocked", "BLOCKED", false, 1},
		{"rejected", "REJECTED", false, 1}, {"unknown", "UNKNOWN", false, 1},
		{"changed source", "RECONCILED", true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := orderInventoryDBFixture(t)
			posting := orderInventoryPosting(t, fixture, "attention-posting", "awaiting_packaging")
			seedPostingFilterWork(t, fixture, posting, tc.state)
			if tc.changed {
				inventoryExec(t, fixture.db, `UPDATE orders_posting_heads SET projection=jsonb_set(projection,'{evidence,revision}','"changed"'::jsonb)`)
			}
			page, err := fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, orders.PageRequest{Limit: 25, Attention: true})
			if err != nil || len(page.Postings) != tc.count {
				t.Fatalf("attention filter: %+v %v", page, err)
			}
			inventoryExec(t, fixture.db, `UPDATE orders_posting_heads SET projection=jsonb_set(projection,'{order_number}','{"presence":"value","value":"VISIBLE-ORDER-123"}'::jsonb)`)
			for _, status := range []string{"", "awaiting_packaging"} {
				page, err := fixture.db.OrderPostings(fixture.ctx, fixture.config.Scope, orders.PageRequest{Limit: 25, Search: "VISIBLE-ORDER-123", Status: status})
				if err != nil || len(page.Postings) != 1 {
					t.Fatalf("displayed order number search: %+v %v", page, err)
				}
			}
		})
	}
}

func seedPostingFilterWork(t *testing.T, fixture orderInventoryFixture, posting orders.Posting, state string) {
	t.Helper()
	tx, err := fixture.db.Pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(fixture.ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	scope := fixture.config.Scope
	preview := orders.Preview{ID: "filter-preview", Scope: scope, ConfigRevision: fixture.config.Revision}
	work := orders.Work{ID: "filter-work", OperationID: "filter-operation", Scope: scope, ConfigRevision: fixture.config.Revision, State: orders.State(state), CommandFingerprint: strings.Repeat("a", 64), Command: orders.Command{ID: "filter-command", Scope: scope, Action: orders.ActionMirrorCreate, PostingNumber: posting.PostingNumber, ExpectedSourceRevision: posting.Evidence.Revision}}
	exec(`INSERT INTO orders_previews(id,tenant_id,moysklad_account,ozon_account,config_revision,preview,fingerprint,actor,automatic,owner_user_id,created_at,expires_at) SELECT $1,tenant_id,moysklad_account,ozon_account,revision,$2,$3,actor,false,owner_user_id,$4,$5 FROM orders_config_revisions WHERE tenant_id=$6`, preview.ID, inventoryJSON(t, preview), strings.Repeat("a", 64), fixture.now, fixture.now.Add(time.Minute), scope.Tenant)
	exec(`INSERT INTO orders_operations(id,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,owner_user_id,automatic,request_key,request_hash,created_at) SELECT $1,tenant_id,moysklad_account,ozon_account,revision,$2,actor,owner_user_id,false,'filter-request',$3,$4 FROM orders_config_revisions WHERE tenant_id=$5`, work.OperationID, preview.ID, strings.Repeat("a", 64), fixture.now, scope.Tenant)
	exec(`INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'mirror_create',$10,$11,$12,$12)`, work.ID, work.OperationID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, work.ConfigRevision, posting.PostingNumber, work.Command.ID, strings.Repeat("a", 64), state, inventoryJSON(t, work), fixture.now)
	taskState := "failed"
	if state == "UNKNOWN" {
		taskState = "pending"
	}
	if state == "RECONCILED" {
		taskState = "succeeded"
		exec(`INSERT INTO orders_reconciliations(work_id,tenant_id,moysklad_account,ozon_account,correlation,observed_posting,created_at) VALUES($1,$2,$3,$4,'{}'::jsonb,$5,$6)`, work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, inventoryJSON(t, posting), fixture.now)
	}
	exec(`INSERT INTO outbox_tasks(operation_key,task_type,state,orders_operation_id,orders_work_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account,max_attempts) VALUES('filter-task','orders_sync',$6,$1,$2,$3,$4,$5,1)`, work.OperationID, work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, taskState)
	if err = tx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}

}
