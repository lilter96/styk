//go:build integration

package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/orders"
)

// These fixtures exercise relational admission only. They contain unexecuted
// draft intents, never fabricated provider acknowledgements or reconciliations.
// The HTTP acceptance tests supply real adapter rejection/concurrency journeys.
type nativeRetryPGFixture struct {
	orderInventoryFixture
	binding orders.Correlation
}

func newNativeRetryPGFixture(t *testing.T) nativeRetryPGFixture {
	t.Helper()
	f, _ := nativePointStoreFixture(t)
	posting := orderInventoryPosting(t, f, "retry-db-posting", "awaiting_packaging")
	binding, err := readNativePointBinding(f.ctx, f.db.Pool, f.config.Scope, posting.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	binding.CustomerOrderID = "11111111-1111-4111-8111-111111111111"
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	if err = resolveOrderProjection(f.ctx, tx, binding, f.now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return nativeRetryPGFixture{orderInventoryFixture: f, binding: binding}
}

func (f nativeRetryPGFixture) work(t *testing.T, id string, retry *orders.NativeRetryAuthorization) orders.Work {
	t.Helper()
	correlation, err := orders.NativeCorrelationFor(f.config.Scope, orders.ActionDemandCreate, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	c := orders.Command{ID: id + "-command", Scope: f.config.Scope, Action: orders.ActionDemandCreate, Correlation: f.binding, PostingNumber: f.binding.PostingNumber, CommercialOrderID: "commercial", ConfigRevision: f.config.Revision, CreatedAt: f.now.Add(-time.Hour), NativeMovement: &orders.NativeMovement{Correlation: correlation, Event: orders.NativeMovementEvent{Digest: strings.Repeat("a", 64)}, Applicable: orders.Present(false), Retry: retry}}
	return orders.Work{ID: id, OperationID: id + "-operation", Scope: f.config.Scope, PreviewID: id + "-preview", ConfigRevision: f.config.Revision, Command: c, CommandFingerprint: strings.Repeat("b", 64), State: orders.StateQueued}
}

func (f nativeRetryPGFixture) seedWork(t *testing.T, tx pgx.Tx, w orders.Work) time.Time {
	t.Helper()
	now, err := ordersClock(f.ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	preview := orders.Preview{ID: w.PreviewID, Scope: w.Scope, ConfigRevision: w.ConfigRevision}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO orders_previews(id,tenant_id,moysklad_account,ozon_account,config_revision,preview,fingerprint,actor,automatic,owner_user_id,created_at,expires_at) SELECT $1,tenant_id,moysklad_account,ozon_account,revision,$2,$3,actor,false,owner_user_id,$4,$5 FROM orders_config_revisions WHERE tenant_id=$6 AND revision=$7`, []any{w.PreviewID, inventoryJSON(t, preview), w.CommandFingerprint, now, now.Add(time.Hour), w.Scope.Tenant, w.ConfigRevision}},
		{`INSERT INTO orders_operations(id,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,owner_user_id,automatic,request_key,request_hash,created_at) SELECT $1,tenant_id,moysklad_account,ozon_account,revision,$2,actor,owner_user_id,false,$1,$3,$4 FROM orders_config_revisions WHERE tenant_id=$5 AND revision=$6`, []any{w.OperationID, w.PreviewID, w.CommandFingerprint, now, w.Scope.Tenant, w.ConfigRevision}},
		{`INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)`, []any{w.ID, w.OperationID, w.Scope.Tenant, w.Scope.MoyskladAccount, w.Scope.OzonAccount, w.ConfigRevision, w.Command.PostingNumber, w.Command.ID, w.CommandFingerprint, w.Command.Action, w.State, inventoryJSON(t, w), now}},
		{`INSERT INTO outbox_tasks(operation_key,task_type,orders_operation_id,orders_work_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account,max_attempts,available_at) VALUES($1,'orders_sync',$2,$3,$4,$5,$6,1,$7)`, []any{"retry-db:" + w.ID, w.OperationID, w.ID, w.Scope.Tenant, w.Scope.MoyskladAccount, w.Scope.OzonAccount, now}},
	}
	for _, statement := range statements {
		if _, err = tx.Exec(f.ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return now
}

func (f nativeRetryPGFixture) insertIntent(tx pgx.Tx, w orders.Work, scope orders.Scope, syncID string, now time.Time) error {
	m := w.Command.NativeMovement
	_, err := tx.Exec(f.ctx, `INSERT INTO orders_native_document_intents(work_id,tenant_id,moysklad_account,ozon_account,kind,posting_number,event_digest,sync_id,marker,customer_order_id,applicable,created_at) VALUES($1,$2,$3,$4,'demand',$5,$6,$7,$8,$9,false,$10)`, w.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, w.Command.PostingNumber, m.Event.Digest, syncID, m.Correlation.Marker, w.Command.Correlation.CustomerOrderID, now)
	return err
}

func (f nativeRetryPGFixture) initial(t *testing.T) (orders.Work, time.Time) {
	t.Helper()
	w := f.work(t, "retry-db-origin", nil)
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	now := f.seedWork(t, tx, w)
	if err = insertNativeCanonicalEvent(f.ctx, tx, w, now); err != nil {
		t.Fatal(err)
	}
	if err = f.insertIntent(tx, w, w.Scope, w.Command.NativeMovement.Correlation.SyncID, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return w, now
}

func (f nativeRetryPGFixture) blockUnsent(t *testing.T) {
	t.Helper()
	now := time.Now().UTC()
	w, err := f.db.ClaimOrderWork(f.ctx, orders.ClaimRequest{WorkerID: "retry-db-worker", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil || w == nil {
		t.Fatal("claim unexecuted draft", err)
	}
	if err = f.db.CompleteOrderWork(f.ctx, orders.Completion{Scope: w.Scope, WorkID: w.ID, ClaimToken: w.ClaimToken, ExpectedState: orders.StateClaimed, State: orders.StateBlocked, Code: "PREWRITE_BLOCKED"}); err != nil {
		t.Fatal(err)
	}
}

func nativeRetryPGDigest(t *testing.T, f nativeRetryPGFixture) string {
	t.Helper()
	var digest string
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT md5(jsonb_build_object('work',(SELECT jsonb_agg(work ORDER BY id) FROM orders_work),'intents',(SELECT jsonb_agg(to_jsonb(i) ORDER BY work_id) FROM orders_native_document_intents i),'credentials',(SELECT jsonb_agg(to_jsonb(c) ORDER BY provider,account_id) FROM connection_credentials c))::text)`).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestNativeRetryPostgresMigrationBackfillsWithoutChangingHistoricalFacts(t *testing.T) {
	f := newNativeRetryPGFixture(t)
	original, created := f.initial(t)
	f.blockUnsent(t)
	before := nativeRetryPGDigest(t, f)
	parts := nativePointMigrationSections(t, "00064_native_document_retries.sql")
	executeNativePointMigration(t, f.orderInventoryFixture, parts[1])
	executeNativePointMigration(t, f.orderInventoryFixture, parts[0])
	if nativeRetryPGDigest(t, f) != before {
		t.Fatal("64 migration changed intents, credentials or work history")
	}
	var origin string
	var count int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*),min(origin_work_id) FROM orders_native_document_events`).Scan(&count, &origin); err != nil || count != 1 || origin != original.ID {
		t.Fatal("canonical history was duplicated or reassigned", err)
	}
	rows, err := f.db.Pool.Query(f.ctx, nativeConsumptionSQL, append(ordersScopeArgs(f.config.Scope), f.binding.PostingNumber, "", "", 2)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var row nativeConsumptionRow
	if !rows.Next() {
		t.Fatal("durable native history missing")
	}
	if err = row.scan(rows); err != nil || !row.created.Equal(created) || row.created.Equal(original.Command.CreatedAt) {
		t.Fatal("preview clock substituted for database work admission", err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("native history unexpectedly duplicated")
	}
}

func (f nativeRetryPGFixture) attempt(t *testing.T, w orders.Work, scope orders.Scope, syncID string, link bool) error {
	t.Helper()
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	now := f.seedWork(t, tx, w)
	if err = f.insertIntent(tx, w, scope, syncID, now); err != nil {
		return err
	}
	if link {
		if err = insertNativeRetryAuthorization(f.ctx, tx, w); err != nil {
			return err
		}
	}
	return tx.Commit(f.ctx)
}

func TestNativeRetryPostgresCanonicalScopeAndNonForkingAuthorization(t *testing.T) {
	f := newNativeRetryPGFixture(t)
	original, _ := f.initial(t)
	f.blockUnsent(t)
	duplicate := f.work(t, "ordinary-duplicate", nil)
	if err := f.attempt(t, duplicate, duplicate.Scope, duplicate.Command.NativeMovement.Correlation.SyncID, false); err == nil {
		t.Fatal("ordinary duplicate event admitted without authorization")
	}
	f.assertSubstitutionsRejected(t)
	retry := &orders.NativeRetryAuthorization{PreviousWorkID: original.ID, PreviousCommandFingerprint: original.CommandFingerprint, EventDigest: original.Command.NativeMovement.Event.Digest}
	accepted := f.work(t, "authorized-retry", retry)
	if err := f.attempt(t, accepted, accepted.Scope, accepted.Command.NativeMovement.Correlation.SyncID, true); err != nil {
		t.Fatal("explicit proven unsent retry rejected", err)
	}
	f.blockUnsent(t)
	fork := f.work(t, "retry-fork", retry)
	if err := f.attempt(t, fork, fork.Scope, fork.Command.NativeMovement.Correlation.SyncID, true); !errors.Is(err, orders.ErrConflict) {
		t.Fatal("retry predecessor fork was not prevented", err)
	}
	f.assertRetryRollbackRefused(t)
}

func (f nativeRetryPGFixture) assertRetryRollbackRefused(t *testing.T) {
	t.Helper()
	parts := nativePointMigrationSections(t, "00064_native_document_retries.sql")
	if _, err := f.db.Pool.Exec(f.ctx, parts[1]); err == nil || !strings.Contains(err.Error(), "native retry authorization history prevents rollback") {
		t.Fatal("retry audit was discarded by rollback", err)
	}
	var count int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_native_document_retries`).Scan(&count); err != nil || count != 1 {
		t.Fatal("retry history changed", err)
	}
}

func (f nativeRetryPGFixture) assertSubstitutionsRejected(t *testing.T) {
	t.Helper()
	for _, change := range []string{"tenant", "account", "sync"} {
		w := f.work(t, "wrong-"+change, nil)
		scope, syncID := w.Scope, w.Command.NativeMovement.Correlation.SyncID
		if change == "tenant" {
			scope.Tenant = "another-tenant"
		}
		if change == "account" {
			scope.OzonAccount = "another-account"
		}
		if change == "sync" {
			syncID = "22222222-2222-4222-8222-222222222222"
		}
		var constraint *pgconn.PgError
		if err := f.attempt(t, w, scope, syncID, false); !errors.As(err, &constraint) || constraint.Code != "23503" {
			t.Fatal("canonical identity foreign key did not reject substitution", err)
		}
	}
}
