//go:build integration

package integration

import (
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersUnmirroredUnassembledUnknownNativeWithoutResolvedBindingStillBlocks(t *testing.T) {
	f, provider, splitPreview := unassembledHTTPFixture(t)
	nativePreview := f.preview(t) // A real validated native proposal before the split.
	command := nativePreview.Decisions[0].Commands[0]
	if command.Action != orders.ActionMirrorCreate || command.Correlation.CustomerOrderID != "" {
		t.Fatal("fixture did not establish unresolved native create")
	}
	f.approve(t, splitPreview)
	f.run(t, orders.StateAcknowledged)
	f.run(t, orders.StatePolling)
	f.run(t, orders.StateReconciled)
	split := f.latestWork(t)
	seedUnresolvedNativeHistory(t, f, nativePreview, command)
	child := unmirroredUnassembledHTTPReview(t, f, split.Receipt.PostingNumbers[0])
	if child.Decisions[0].Ready || len(child.Decisions[0].Commands) != 0 {
		t.Fatal("unresolved lost native creation was mistaken for native absence")
	}
	if provider.writes.Load() != 1 {
		t.Fatal("UNKNOWN history caused split replay")
	}
}

// New immutable fixture rows model a historical lost create acknowledgment.
// No completed work, receipt or original audit is edited to produce the case.
func seedUnresolvedNativeHistory(t *testing.T, f positionHTTPFixture, preview orders.Preview, command orders.Command) {
	t.Helper()
	work := orders.Work{ID: "lost-native-parent", OperationID: "lost-native-operation", PreviewID: preview.ID, Scope: f.config.Scope, ConfigRevision: f.config.Revision, State: orders.StateUnknown, DispatchCount: 1, Command: command, Receipt: orders.Receipt{CommandID: command.ID, State: orders.StateUnknown, ObservedAt: time.Now().UTC()}}
	var err error
	work.CommandFingerprint = unmirroredNativeFingerprint(t, command)
	raw, err := json.Marshal(work)
	if err != nil {
		t.Fatal(err)
	}
	commandRaw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(f.ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Error("fixture rollback failed")
		}
	}()
	scope := f.config.Scope
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err = tx.Exec(f.ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO orders_operations(id,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,owner_user_id,automatic,request_key,request_hash,created_at) SELECT $1,tenant_id,moysklad_account,ozon_account,config_revision,$2,actor,owner_user_id,false,'lost-parent-native',$3,clock_timestamp() FROM orders_operations WHERE id=(SELECT operation_id FROM orders_work WHERE id=$4)`, work.OperationID, preview.ID, work.CommandFingerprint, f.latestWork(t).ID)
	exec(`INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,dispatch_count,dispatched_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'mirror_create','UNKNOWN',$10,1,clock_timestamp(),clock_timestamp(),clock_timestamp())`, work.ID, work.OperationID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, work.ConfigRevision, command.PostingNumber, command.ID, work.CommandFingerprint, raw)
	exec(`INSERT INTO orders_write_intents(work_id,tenant_id,moysklad_account,ozon_account,command,fingerprint,created_at) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp())`, work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, commandRaw, work.CommandFingerprint)
	exec(`INSERT INTO orders_attempts(work_id,tenant_id,moysklad_account,ozon_account,phase,code,created_at) VALUES($1,$2,$3,$4,'DISPATCHING','',clock_timestamp())`, work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount)
	exec(`INSERT INTO outbox_tasks(operation_key,task_type,state,orders_operation_id,orders_work_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account,max_attempts) VALUES('lost-parent-native','orders_sync','pending',$1,$2,$3,$4,$5,1)`, work.OperationID, work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount)
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func unmirroredNativeFingerprint(t *testing.T, command orders.Command) string {
	t.Helper()
	result, err := orders.CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
