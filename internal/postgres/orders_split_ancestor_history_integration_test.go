//go:build integration

package postgres

import "testing"

func TestNestedNativeHistoryUsesBoundedActualExistenceWitness(t *testing.T) {
	f := orderInventoryDBFixture(t)
	posting := orderInventoryPosting(t, f, "native-history-posting", "awaiting_packaging")
	orderInventoryReconciled(t, f, posting, "1")
	cloneNativeHistoryFixture(t, f)
	var count int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_work WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3 AND posting_number=$4`, append(ordersScopeArgs(f.config.Scope), posting.PostingNumber)...).Scan(&count); err != nil || count != 2 {
		t.Fatal("actual repeated native history fixture", err)
	}
	history, err := readNestedSplitNativeHistory(f.ctx, f.db.Pool, f.config.Scope, []string{posting.PostingNumber, "unmirrored-posting"})
	if err != nil || len(history) != 2 {
		t.Fatal("read bounded scoped native witnesses", err)
	}
	for _, item := range history {
		if item.MirrorWorkIDs == nil {
			t.Fatal("missing traversal represented as no history")
		}
		if item.PostingNumber == posting.PostingNumber {
			if len(item.MirrorWorkIDs) != 1 || item.MirrorWorkIDs[0] != "native-work" && item.MirrorWorkIDs[0] != "native-work-extra" {
				t.Fatal("native witness was fabricated, absent or unbounded")
			}
		} else if len(item.MirrorWorkIDs) != 0 {
			t.Fatal("history leaked across posting identities")
		}
	}
	foreign := f.config.Scope
	foreign.OzonAccount = "other-account"
	history, err = readNestedSplitNativeHistory(f.ctx, f.db.Pool, foreign, []string{posting.PostingNumber})
	if err != nil || len(history) != 1 || len(history[0].MirrorWorkIDs) != 0 {
		t.Fatal("native witness crossed account scope", err)
	}
}

// SQL fixtures exercise persistence constraints, not an alleged provider write.
func cloneNativeHistoryFixture(t *testing.T, f orderInventoryFixture) {
	t.Helper()
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	statements := []string{
		`INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,created_at,updated_at)
 SELECT 'native-work-extra',operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,'native-command-extra',command_fingerprint,action,state,jsonb_set(jsonb_set(work,'{id}','"native-work-extra"'),'{command,id}','"native-command-extra"'),created_at,updated_at FROM orders_work WHERE id='native-work'`,
		`INSERT INTO outbox_tasks(operation_key,task_type,state,orders_operation_id,orders_work_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account,max_attempts)
 SELECT 'native-extra-reconciliation','orders_sync','succeeded',operation_id,id,tenant_id,moysklad_account,ozon_account,1 FROM orders_work WHERE id='native-work-extra'`,
		`INSERT INTO orders_reconciliations(work_id,tenant_id,moysklad_account,ozon_account,correlation,observed_target,created_at)
 SELECT 'native-work-extra',tenant_id,moysklad_account,ozon_account,correlation,observed_target,created_at FROM orders_reconciliations WHERE work_id='native-work'`,
	}
	for _, statement := range statements {
		if _, err = tx.Exec(f.ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}
