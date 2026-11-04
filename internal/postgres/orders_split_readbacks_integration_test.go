//go:build integration

package postgres

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

type splitReadbackTestFixture struct {
	orderInventoryFixture
	work   orders.Work
	before orders.Posting
}

func splitReadbackFixture(t *testing.T) splitReadbackTestFixture {
	t.Helper()
	fixture := orderInventoryDBFixture(t)
	before := orderInventoryPosting(t, fixture, "split-source", "awaiting_packaging")
	binding, err := orders.CorrelationFor(fixture.config.Scope, before.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	now := fixture.now
	command := orders.Command{ID: "split-read-command", Scope: fixture.config.Scope, Action: orders.ActionPostingSplitUnassembled, PostingNumber: before.PostingNumber, CommercialOrderID: before.CommercialOrderID, Correlation: binding, ExpectedSourceRevision: before.Evidence.Revision}
	products := orders.Present([]orders.SplitResponseProduct{{SKU: "2001", Quantity: "1"}})
	ack := &orders.UnassembledSplitAck{Postings: orders.Present([]orders.SplitResponsePosting{{PostingNumber: orders.Present("child-a"), Products: products}, {PostingNumber: orders.Present("child-b"), Products: products}})}
	work := orders.Work{ID: "split-read-work", OperationID: "split-read-operation", Scope: fixture.config.Scope, ConfigRevision: fixture.config.Revision, State: orders.StatePolling, DispatchCount: 1, Command: command, CommandFingerprint: strings.Repeat("a", 64), Receipt: orders.Receipt{CommandID: command.ID, State: orders.StateAcknowledged, HTTPStatus: 200, ObservedAt: now.Add(-time.Second), UnassembledSplit: ack}}
	result := splitReadbackTestFixture{orderInventoryFixture: fixture, work: work, before: before}
	splitReadbackSeedWork(t, result)
	return result
}

func splitReadbackSeedWork(t *testing.T, f splitReadbackTestFixture) {
	t.Helper()
	scope := f.config.Scope
	preview := orders.Preview{ID: "split-read-preview", Scope: scope, ConfigRevision: f.config.Revision}
	inventoryExec(t, f.db, `INSERT INTO orders_previews(id,tenant_id,moysklad_account,ozon_account,config_revision,preview,fingerprint,actor,automatic,owner_user_id,created_at,expires_at) SELECT $1,tenant_id,moysklad_account,ozon_account,revision,$2,$3,actor,false,owner_user_id,$4,$5 FROM orders_config_revisions WHERE tenant_id=$6`, preview.ID, inventoryJSON(t, preview), strings.Repeat("a", 64), f.now.Add(-time.Minute), f.now.Add(time.Minute), scope.Tenant)
	inventoryExec(t, f.db, `INSERT INTO orders_operations(id,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,owner_user_id,automatic,request_key,request_hash,created_at) SELECT $1,tenant_id,moysklad_account,ozon_account,revision,$2,actor,owner_user_id,false,'split-read-request',$3,$4 FROM orders_config_revisions WHERE tenant_id=$5`, f.work.OperationID, preview.ID, strings.Repeat("a", 64), f.now.Add(-time.Second), scope.Tenant)
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	if _, err = tx.Exec(f.ctx, `INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,dispatch_count,dispatched_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,1,$13,$13,$13)`, f.work.ID, f.work.OperationID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, f.config.Revision, f.work.Command.PostingNumber, f.work.Command.ID, f.work.CommandFingerprint, f.work.Command.Action, f.work.State, inventoryJSON(t, f.work), f.work.Receipt.ObservedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO outbox_tasks(operation_key,task_type,state,orders_operation_id,orders_work_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account,max_attempts) VALUES('split-read-outbox','orders_sync','pending',$1,$2,$3,$4,$5,1)`, f.work.OperationID, f.work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount); err != nil {
		t.Fatal(err)
	}
	seedSplitReadbackDispatch(t, f, tx)
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func (f splitReadbackTestFixture) observed(t *testing.T, number string, checked time.Time) orders.SplitReadback {
	t.Helper()
	posting := f.before
	posting.PostingNumber = number
	posting.Status = orders.Present("awaiting_packaging") // A read fact, never authority to repeat assembly.
	posting.Evidence.ObservedAt, posting.Evidence.CheckedAt = checked, checked
	posting.Evidence.ProviderUpdatedAt = orders.Present(checked)
	posting.Lines = append([]orders.PostingLine(nil), posting.Lines...)
	posting.Lines[0].Quantity = "1"
	var err error
	posting.Evidence.Revision, err = orders.PostingFingerprint(posting)
	if err != nil {
		t.Fatal(err)
	}
	return orders.SplitReadback{PostingNumber: number, Posting: &posting, Code: "OBSERVED", CheckedAt: checked}
}

func (f splitReadbackTestFixture) save(t *testing.T, cells []orders.SplitReadback) error {
	t.Helper()
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	now, err := ordersClock(f.ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveUnassembledSplitReadbacks(f.ctx, tx, f.work, orders.Completion{SplitReads: cells}, now); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}

func TestSplitReadbacksPersistPointFactsAndClearFailuresWithoutLosingHistory(t *testing.T) {
	f := splitReadbackFixture(t)
	first := f.observed(t, "child-a", f.now.Add(-500*time.Millisecond))
	first.Posting.PII = &orders.Customer{Name: orders.Present("PRIVATE_RECIPIENT")}
	if err := f.save(t, []orders.SplitReadback{first}); err != nil {
		t.Fatal(err)
	}
	assertSplitReadbackIdempotentClock(t, f, first)
	cells, err := f.db.OrderSplitReadbacks(f.ctx, f.config.Scope, f.work.ID)
	if err != nil || len(cells) != 1 || cells[0].Posting == nil || cells[0].Posting.PII != nil || cells[0].Posting.Customer.Reference != "" {
		t.Fatal("scoped readback roundtrip", err)
	}
	failed := orders.SplitReadback{PostingNumber: "child-a", Code: "SOURCE_UNAVAILABLE", CheckedAt: f.now.Add(-300 * time.Millisecond)}
	if err = f.save(t, []orders.SplitReadback{failed}); err != nil {
		t.Fatal(err)
	}
	cells, err = f.db.OrderSplitReadbacks(f.ctx, f.config.Scope, f.work.ID)
	if err != nil || len(cells) != 1 || cells[0].Code != "SOURCE_UNAVAILABLE" || cells[0].Posting != nil {
		t.Fatal("failed fresh read retained stale success", err)
	}
	if err = f.save(t, []orders.SplitReadback{first}); !errors.Is(err, orders.ErrStale) {
		t.Fatal("older success erased later failure", err)
	}
	recovered := f.observed(t, "child-a", f.now.Add(-100*time.Millisecond))
	if err = f.save(t, []orders.SplitReadback{recovered}); err != nil {
		t.Fatal(err)
	}
	cells, err = f.db.OrderSplitReadbacks(f.ctx, f.config.Scope, f.work.ID)
	if err != nil || cells[0].Posting == nil || cells[0].Posting.Evidence.Revision != recovered.Posting.Evidence.Revision {
		t.Fatal("fresh recovery did not clear failure", err)
	}
	var count int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_posting_observations WHERE posting_number='child-a'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("immutable history lost or duplicated", err)
	}
}

func TestSplitReadbacksAreScopedBoundedAndAtomic(t *testing.T) {
	f := splitReadbackFixture(t)
	valid := f.observed(t, "child-a", f.now.Add(-time.Millisecond))
	for _, field := range []string{"tenant", "moy", "ozon"} {
		scope := f.config.Scope
		switch field {
		case "tenant":
			scope.Tenant = "other"
		case "moy":
			scope.MoyskladAccount = "other"
		case "ozon":
			scope.OzonAccount = "other"
		}
		if _, err := f.db.OrderSplitReadbacks(f.ctx, scope, f.work.ID); !errors.Is(err, orders.ErrNotFound) {
			t.Fatal("cross-scope work existence leaked", err)
		}
	}
	invalid := f.observed(t, "unapproved-posting", valid.CheckedAt)
	if err := f.save(t, []orders.SplitReadback{valid, invalid}); !errors.Is(err, orders.ErrStale) {
		t.Fatal("unapproved read target admitted", err)
	}
	cells, err := f.db.OrderSplitReadbacks(f.ctx, f.config.Scope, f.work.ID)
	if err != nil || len(cells) != 0 {
		t.Fatal("partial invalid batch persisted", err)
	}
	var sourceCount int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_posting_observations WHERE posting_number='child-a'`).Scan(&sourceCount); err != nil || sourceCount != 0 {
		t.Fatal("invalid batch leaked source facts", err)
	}
	cells = make([]orders.SplitReadback, 11)
	for i := range cells {
		cells[i] = valid
	}
	if err = f.save(t, cells); !errors.Is(err, orders.ErrInvalid) {
		t.Fatal("per-phase network read bound missing", err)
	}
}

func TestSplitReadbacksRejectClocksDuplicatesAndForgedEvidence(t *testing.T) {
	f := splitReadbackFixture(t)
	for _, name := range []string{"before_ack", "future", "wrong_scope", "wrong_order", "wrong_key", "wrong_code", "wrong_read_clock"} {
		t.Run(name, func(t *testing.T) {
			cell := f.observed(t, "child-a", f.now.Add(-time.Millisecond))
			switch name {
			case "before_ack":
				cell.CheckedAt = f.work.Receipt.ObservedAt.Add(-time.Millisecond)
			case "future":
				cell.CheckedAt = time.Now().Add(time.Hour)
			case "wrong_scope":
				cell.Posting.Scope.Tenant = "other"
			case "wrong_order":
				cell.Posting.CommercialOrderID = "other"
			case "wrong_key":
				cell.Posting.PostingNumber = "child-b"
			case "wrong_code":
				cell.Code = "PRIVATE_RAW_ERROR"
			case "wrong_read_clock":
				cell.Posting.Evidence.CheckedAt = cell.CheckedAt.Add(-time.Second)
			}
			if err := f.save(t, []orders.SplitReadback{cell}); !errors.Is(err, orders.ErrStale) {
				t.Fatal("untrusted split read admitted", err)
			}
		})
	}
	valid := f.observed(t, "child-a", f.now.Add(-time.Millisecond))
	if err := f.save(t, []orders.SplitReadback{valid, valid}); !errors.Is(err, orders.ErrStale) {
		t.Fatal("duplicate target read accepted", err)
	}
}

func seedSplitReadbackDispatch(t *testing.T, f splitReadbackTestFixture, tx pgx.Tx) {
	t.Helper()
	scope := f.config.Scope
	if _, err := tx.Exec(f.ctx, `INSERT INTO orders_write_intents(work_id,tenant_id,moysklad_account,ozon_account,command,fingerprint,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, f.work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, inventoryJSON(t, f.work.Command), f.work.CommandFingerprint, f.work.Receipt.ObservedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(f.ctx, `INSERT INTO orders_attempts(work_id,tenant_id,moysklad_account,ozon_account,phase,code,created_at) VALUES($1,$2,$3,$4,'DISPATCHING','',$5)`, f.work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, f.work.Receipt.ObservedAt); err != nil {
		t.Fatal(err)
	}
}

func assertSplitReadbackIdempotentClock(t *testing.T, f splitReadbackTestFixture, cell orders.SplitReadback) {
	t.Helper()
	var before, after time.Time
	query := `SELECT recorded_at FROM orders_split_readbacks WHERE work_id=$1 AND posting_number=$2`
	if err := f.db.Pool.QueryRow(f.ctx, query, f.work.ID, cell.PostingNumber).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := f.save(t, []orders.SplitReadback{cell}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Pool.QueryRow(f.ctx, query, f.work.ID, cell.PostingNumber).Scan(&after); err != nil || !before.Equal(after) {
		t.Fatal("replay fabricated fresh read admission", err)
	}
}

func TestSplitReadbacksMigrationRollbackPreservesHistory(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/00061_order_unassembled_split.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("migration rollback missing")
	}
	empty := orderInventoryDBFixture(t)
	if _, err = empty.db.Pool.Exec(empty.ctx, parts[1]); err != nil {
		t.Fatal("empty rollback failed", err)
	}
	if _, err = empty.db.Pool.Exec(empty.ctx, parts[0]); err != nil {
		t.Fatal("upgrade after empty rollback failed", err)
	}
	f := splitReadbackFixture(t)
	if err = f.save(t, []orders.SplitReadback{f.observed(t, "child-a", f.now.Add(-time.Millisecond))}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(f.ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded normalized read history")
	}
	cells, err := f.db.OrderSplitReadbacks(f.ctx, f.config.Scope, f.work.ID)
	if err != nil || len(cells) != 1 || cells[0].Posting == nil {
		t.Fatal("refused rollback damaged read history", err)
	}
}

func TestSplitReadbacksDoNotHideNewerOrQuarantinedSourceHeads(t *testing.T) {
	for _, quarantine := range []bool{false, true} {
		t.Run(map[bool]string{false: "newer_head", true: "quarantined_head"}[quarantine], func(t *testing.T) {
			f := splitReadbackFixture(t)
			old := f.observed(t, "child-a", f.now.Add(-500*time.Millisecond))
			if err := f.save(t, []orders.SplitReadback{old}); err != nil {
				t.Fatal(err)
			}
			changed := f.observed(t, "child-a", f.now.Add(-100*time.Millisecond))
			if quarantine {
				changed.Posting.Evidence.ProviderUpdatedAt = old.Posting.Evidence.ProviderUpdatedAt
			}
			changed.Posting.Status = orders.Present("delivering")
			var err error
			changed.Posting.Evidence.Revision, err = orders.PostingFingerprint(*changed.Posting)
			if err != nil {
				t.Fatal(err)
			}
			admitSplitReadbackSource(t, f, *changed.Posting)
			cells, err := f.db.OrderSplitReadbacks(f.ctx, f.config.Scope, f.work.ID)
			if err != nil || len(cells) != 1 || cells[0].Code != "SOURCE_CHANGED" || cells[0].Posting != nil {
				t.Fatal("cached split observation hid changed source authority", err)
			}
			var code, revision string
			err = f.db.Pool.QueryRow(f.ctx, `SELECT r.read_code,o.source_revision FROM orders_split_readbacks r JOIN orders_posting_observations o ON o.id=r.observation_id WHERE r.work_id=$1`, f.work.ID).Scan(&code, &revision)
			if err != nil || code != "OBSERVED" || revision != old.Posting.Evidence.Revision {
				t.Fatal("read path rewrote historical point reference", err)
			}
		})
	}
}

func admitSplitReadbackSource(t *testing.T, f splitReadbackTestFixture, posting orders.Posting) {
	t.Helper()
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	now, err := ordersClock(f.ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = insertTimedOrderPosting(f.ctx, tx, posting, orderObservationClock{RecordedAt: now, CheckedAt: posting.Evidence.CheckedAt}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSplitReadbacksJoinClaimTransactionAndRollbackAtomically(t *testing.T) {
	f := splitReadbackFixture(t)
	cell := f.observed(t, "child-a", f.now.Add(-time.Millisecond))
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	now, err := ordersClock(f.ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveUnassembledSplitReadbacks(f.ctx, tx, f.work, orders.Completion{SplitReads: []orders.SplitReadback{cell}}, now); err != nil {
		t.Fatal(err)
	}
	inside, err := readOrderSplitReadbacks(f.ctx, tx, f.config.Scope, f.work.ID)
	if err != nil || len(inside) != 1 || inside[0].Posting == nil {
		t.Fatal("claim transaction cannot validate its own read cells", err)
	}
	outside, err := f.db.OrderSplitReadbacks(f.ctx, f.config.Scope, f.work.ID)
	if err != nil || len(outside) != 0 {
		t.Fatal("uncommitted read cells escaped claim transaction", err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	outside, err = f.db.OrderSplitReadbacks(f.ctx, f.config.Scope, f.work.ID)
	if err != nil || len(outside) != 0 {
		t.Fatal("rollback retained split progress", err)
	}
	var count int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_posting_observations WHERE posting_number='child-a'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rollback retained source read facts", err)
	}
}
