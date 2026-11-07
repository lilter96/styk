//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
)

func merchantJournalFixture(t *testing.T) (orderInventoryFixture, orders.MerchantIntent) {
	t.Helper()
	f := orderInventoryDBFixture(t)
	posting := orderInventoryPosting(t, f, "merchant-schema-posting", "awaiting_packaging")
	binding := orderInventoryReconciled(t, f, posting, "3")
	intent := orders.MerchantIntent{Scope: f.config.Scope, Correlation: binding, BeforeStateID: "00000000-0000-4000-8000-000000007201", AfterStateID: "00000000-0000-4000-8000-000000007202",
		BeforeRevision: "merchant-before", AfterRevision: "merchant-after", BeforeUpdatedAt: "2026-11-06 12:00:00.000", AfterUpdatedAt: "2026-11-06 12:01:00.000", ObservedAt: f.now,
		ConfigRevision: f.config.Revision, CredentialRevision: 1, Action: orders.ActionPostingPack, Scheme: "FBS", EventKind: "assembly_ready", WholePosting: true}
	return f, merchantJournalIdentity(t, intent, 1)
}

func merchantJournalIdentity(t *testing.T, intent orders.MerchantIntent, number int) orders.MerchantIntent {
	t.Helper()
	intent.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", 720000+number)
	correlation, err := orders.CorrelationFor(intent.Scope, intent.Correlation.PostingNumber)
	if err != nil {
		t.Fatal(err)
	}
	correlation.CustomerOrderID = intent.Correlation.CustomerOrderID
	intent.Correlation = correlation
	intent.Digest, err = intent.Fingerprint()
	if err != nil || intent.Validate() != nil {
		t.Fatal("invalid synthetic merchant journal facts", err)
	}
	return intent
}

func merchantJournalInsert(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, intent orders.MerchantIntent, state string, work *string) error {
	raw, err := ordersJSON(intent)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO orders_merchant_intents(id,tenant_id,moysklad_account,ozon_account,posting_number,customer_order_id,config_revision,digest,intent,state,work_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, intent.ID, intent.Scope.Tenant, intent.Scope.MoyskladAccount, intent.Scope.OzonAccount, intent.Correlation.PostingNumber, intent.Correlation.CustomerOrderID, intent.ConfigRevision, intent.Digest, raw, state, work)
	return err
}

func merchantJournalSQLState(t *testing.T, err error, wanted string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != wanted {
		t.Fatalf("constraint error = %v, want SQLSTATE %s", err, wanted)
	}
}

func TestMerchantIntentPostgresScopedReferencesAndSourceActionDedupe(t *testing.T) {
	f, intent := merchantJournalFixture(t)
	if err := merchantJournalInsert(f.ctx, f.db.Pool, intent, "OBSERVED", nil); err != nil {
		t.Fatal(err)
	}
	duplicate := intent
	duplicate.AfterUpdatedAt = "2026-11-06 12:02:00.000"
	duplicate = merchantJournalIdentity(t, duplicate, 2)
	merchantJournalSQLState(t, merchantJournalInsert(f.ctx, f.db.Pool, duplicate, "OBSERVED", nil), "23505")
	separate := intent
	separate.Action, separate.EventKind = orders.ActionDemandCreate, "handoff_confirmed"
	separate = merchantJournalIdentity(t, separate, 3)
	if err := merchantJournalInsert(f.ctx, f.db.Pool, separate, "OBSERVED", nil); err != nil {
		t.Fatal("distinct action at the same source revision rejected", err)
	}
	missingConfig := merchantJournalIdentity(t, intent, 4)
	missingConfig.ConfigRevision += 100
	missingConfig = merchantJournalIdentity(t, missingConfig, 4)
	missingConfig.AfterRevision = "missing-config-revision"
	missingConfig = merchantJournalIdentity(t, missingConfig, 4)
	merchantJournalSQLState(t, merchantJournalInsert(f.ctx, f.db.Pool, missingConfig, "OBSERVED", nil), "23503")
	other := merchantJournalOtherScope(t, f)
	foreign := intent
	foreign.Scope, foreign.Correlation.Scope, foreign.ConfigRevision = other.config.Scope, other.config.Scope, other.config.Revision
	foreign = merchantJournalIdentity(t, foreign, 5)
	foreignWork := "native-work"
	merchantJournalSQLState(t, merchantJournalInsert(f.ctx, f.db.Pool, foreign, "QUEUED", &foreignWork), "23503")
	if err := merchantJournalInsert(f.ctx, f.db.Pool, foreign, "OBSERVED", nil); err != nil {
		t.Fatal("valid independent account-pair evidence rejected", err)
	}
	items, err := f.db.OrderMerchantIntents(f.ctx, f.config.Scope, 10)
	if err != nil || len(items) != 2 {
		t.Fatal("journal read leaked another account-pair", len(items), err)
	}
}

func merchantJournalOtherScope(t *testing.T, f orderInventoryFixture) orderInventoryFixture {
	t.Helper()
	other := f
	other.config.Scope.OzonAccount = "merchant-other-ozon"
	scope := other.config.Scope
	if err := f.db.RegisterStockConnection(f.ctx, scope.Tenant, "ozon", scope.OzonAccount, "synthetic merchant journal FK", true); err != nil {
		t.Fatal(err)
	}
	inventoryExec(t, f.db, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,ciphertext,checked_at,last_request_id) VALUES($1,'ozon',$2,'Synthetic',1,$3,clock_timestamp(),'merchant-journal-other')`, scope.Tenant, scope.OzonAccount, make([]byte, 29))
	actor, found := identity.SessionFromContext(f.ctx)
	if !found {
		t.Fatal("fixture owner unavailable")
	}
	other.config.Revision = 0
	var err error
	other.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: other.config, Actor: actor.AuditID, IdempotencyKey: "merchant-other-config"}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	orderInventoryPosting(t, other, "merchant-schema-posting", "awaiting_packaging")
	return other
}

func TestMerchantIntentPostgresNewConfigCannotReplaySameSourceAction(t *testing.T) {
	f, intent := merchantJournalFixture(t)
	if err := merchantJournalInsert(f.ctx, f.db.Pool, intent, "OBSERVED", nil); err != nil {
		t.Fatal(err)
	}
	actor, found := identity.SessionFromContext(f.ctx)
	if !found {
		t.Fatal("fixture owner unavailable")
	}
	config, err := f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: actor.AuditID, IdempotencyKey: "merchant-source-reconfigured"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	replay := intent
	replay.ConfigRevision = config.Revision
	replay = merchantJournalIdentity(t, replay, 8)
	if replay.Digest == intent.Digest {
		t.Fatal("fixture must represent a changed configuration digest")
	}
	merchantJournalSQLState(t, merchantJournalInsert(f.ctx, f.db.Pool, replay, "OBSERVED", nil), "23505")
	items, err := f.db.OrderMerchantIntents(f.ctx, intent.Scope, 10)
	if err != nil || len(items) != 1 || items[0].Intent.Digest != intent.Digest {
		t.Fatal("new policy revision duplicated an old source action", err)
	}
}

func TestMerchantIntentPostgresPayloadAndTerminalBindingAreImmutable(t *testing.T) {
	f, intent := merchantJournalFixture(t)
	if err := merchantJournalInsert(f.ctx, f.db.Pool, intent, "OBSERVED", nil); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct{ statement, code string }{
		{`UPDATE orders_merchant_intents SET intent=jsonb_set(intent,'{after_revision}','"forged"') WHERE id=$1`, "P0001"},
		{`UPDATE orders_merchant_intents SET digest=repeat('a',64) WHERE id=$1`, "P0001"},
		{`UPDATE orders_merchant_intents SET customer_order_id='foreign' WHERE id=$1`, "P0001"},
		{`DELETE FROM orders_merchant_intents WHERE id=$1`, "55000"},
	} {
		_, err := f.db.Pool.Exec(f.ctx, change.statement, intent.ID)
		merchantJournalSQLState(t, err, change.code)
	}
	if err := f.db.BlockMerchantIntent(f.ctx, f.config.Scope, intent.ID, "EXPLICIT_INTERVENTION"); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{
		`UPDATE orders_merchant_intents SET state='OBSERVED' WHERE id=$1`,
		`UPDATE orders_merchant_intents SET code='REPLACED' WHERE id=$1`,
		`UPDATE orders_merchant_intents SET state='QUEUED',work_id='native-work' WHERE id=$1`,
	} {
		_, err := f.db.Pool.Exec(f.ctx, change, intent.ID)
		merchantJournalSQLState(t, err, "P0001")
	}
	items, err := f.db.OrderMerchantIntents(f.ctx, intent.Scope, 10)
	if err != nil || len(items) != 1 || items[0].Intent.Digest != intent.Digest || items[0].State != "BLOCKED" || items[0].Code != "EXPLICIT_INTERVENTION" {
		t.Fatal("refused mutation damaged original journal", err)
	}
}

func TestMerchantIntentPostgresConcurrentBindingCASRollsBackLosingWork(t *testing.T) {
	f, intent := merchantJournalFixture(t)
	merchantJournalCurrentPoint(t, &f, &intent)
	merchantJournalCandidateOperation(t, f)
	if err := merchantJournalInsert(f.ctx, f.db.Pool, intent, "OBSERVED", nil); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsOut := make(chan error, 2)
	for index := range 2 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errorsOut <- merchantJournalBindCandidate(f, intent, fmt.Sprintf("merchant-candidate-%d", index))
		}(index)
	}
	wait.Wait()
	successes := 0
	for range 2 {
		err := <-errorsOut
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, orders.ErrBlocked) && !errors.Is(err, orders.ErrConflict) {
			var conflict *pgconn.PgError
			if !errors.As(err, &conflict) || conflict.Code != "23505" || conflict.ConstraintName != "orders_work_active_target" && conflict.ConstraintName != "orders_work_active_fulfillment_target" {
				t.Fatal("unexpected concurrent binding result", err)
			}
		}
	}
	if successes != 1 {
		t.Fatal("concurrent schedulers did not produce one binding", successes)
	}
	var state, winner string
	var count int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT state,work_id FROM orders_merchant_intents WHERE id=$1`, intent.ID).Scan(&state, &winner); err != nil || state != "QUEUED" {
		t.Fatal("durable winner missing", err)
	}
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_work WHERE id LIKE 'merchant-candidate-%'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("losing approval left orphan work", count, err)
	}
	_, err := f.db.Pool.Exec(f.ctx, `UPDATE orders_merchant_intents SET work_id='native-work' WHERE id=$1`, intent.ID)
	merchantJournalSQLState(t, err, "P0001")
	command := orders.Command{Scope: intent.Scope, PostingNumber: intent.Correlation.PostingNumber, MerchantIntent: &intent}
	if err = validateMerchantIntentJournal(f.ctx, f.db.Pool, command, winner); err != nil {
		t.Fatal("bound owner work cannot verify its event", err)
	}
	if err = validateMerchantIntentJournal(f.ctx, f.db.Pool, command, "native-work"); !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("another work item borrowed the event", err)
	}
}

func merchantJournalCurrentPoint(t *testing.T, f *orderInventoryFixture, intent *orders.MerchantIntent) {
	t.Helper()
	actor, found := identity.SessionFromContext(f.ctx)
	if !found {
		t.Fatal("fixture owner unavailable")
	}
	config := f.config
	config.Enabled, config.TestEnabled, config.ReadConfigured, config.ReadEnabled = true, true, true, true
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: actor.AuditID, IdempotencyKey: "merchant-cas-read-permission"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	intent.ConfigRevision = f.config.Revision
	*intent = merchantJournalIdentity(t, *intent, 1)
	read := nativePointReadFixture(t, intent.Correlation, time.Now().UTC().Add(-time.Second))
	read.Point.TargetRevision, read.Point.StateID, read.Point.ProviderUpdatedAt = intent.AfterRevision, intent.AfterStateID, orders.Present(intent.AfterUpdatedAt)
	if err = commitNativePointFixture(t, *f, nativePointPage(t, *f, read, "merchant-cas-point")); err != nil {
		t.Fatal(err)
	}
}

func merchantJournalBindCandidate(f orderInventoryFixture, intent orders.MerchantIntent, id string) error {
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	tx, err := f.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackMerchantMutation(tx)
	work := orders.Work{ID: id, OperationID: "merchant-cas-operation", Scope: intent.Scope, ConfigRevision: f.config.Revision, State: orders.StateQueued, CommandFingerprint: strings.Repeat("c", 64),
		Command: orders.Command{ID: id, Scope: intent.Scope, Action: intent.Action, ConfigRevision: f.config.Revision, PostingNumber: intent.Correlation.PostingNumber, Correlation: intent.Correlation, MerchantIntent: &intent}}
	raw, err := ordersJSON(work)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,created_at,updated_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$1,$8,$9,'QUEUED',$10,clock_timestamp(),clock_timestamp())`, id, work.OperationID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount, work.ConfigRevision, work.Command.PostingNumber, work.CommandFingerprint, work.Command.Action, raw)
	if err != nil {
		return err
	}
	if err = bindMerchantIntentWork(ctx, tx, work); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_tasks(operation_key,task_type,orders_operation_id,orders_work_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account,max_attempts,available_at)
 VALUES($1,'orders_sync',$2,$3,$4,$5,$6,1,clock_timestamp())`, "merchant-cas:"+id, work.OperationID, work.ID, work.Scope.Tenant, work.Scope.MoyskladAccount, work.Scope.OzonAccount)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func merchantJournalCandidateOperation(t *testing.T, f orderInventoryFixture) {
	t.Helper()
	preview := orders.Preview{ID: "merchant-cas-preview", Scope: f.config.Scope, ConfigRevision: f.config.Revision}
	inventoryExec(t, f.db, `INSERT INTO orders_previews(id,tenant_id,moysklad_account,ozon_account,config_revision,preview,fingerprint,actor,automatic,owner_user_id,created_at,expires_at)
 SELECT $1,tenant_id,moysklad_account,ozon_account,revision,$2,$3,actor,false,owner_user_id,clock_timestamp(),clock_timestamp()+interval '1 minute'
 FROM orders_config_revisions WHERE tenant_id=$4 AND revision=$5`, preview.ID, inventoryJSON(t, preview), strings.Repeat("c", 64), f.config.Scope.Tenant, f.config.Revision)
	inventoryExec(t, f.db, `INSERT INTO orders_operations(id,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,owner_user_id,automatic,request_key,request_hash,created_at)
 SELECT 'merchant-cas-operation',tenant_id,moysklad_account,ozon_account,revision,$1,actor,owner_user_id,false,'merchant-cas-request',$2,clock_timestamp()
 FROM orders_config_revisions WHERE tenant_id=$3 AND revision=$4`, preview.ID, strings.Repeat("c", 64), f.config.Scope.Tenant, f.config.Revision)
}

func TestMerchantIntentPostgresMigrationRollbackPreservesEvidence(t *testing.T) {
	for _, history := range []string{"intent", "policy"} {
		t.Run(history, func(t *testing.T) {
			f, intent := merchantJournalFixture(t)
			if history == "intent" {
				if err := merchantJournalInsert(f.ctx, f.db.Pool, intent, "OBSERVED", nil); err != nil {
					t.Fatal(err)
				}
			} else {
				actor, found := identity.SessionFromContext(f.ctx)
				if !found {
					t.Fatal("fixture owner unavailable")
				}
				config := f.config
				config.Lifecycle = &orders.LifecyclePolicy{Enabled: false}
				if _, err := f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: actor.AuditID, IdempotencyKey: "merchant-disabled-policy-history"}, f.now); err != nil {
					t.Fatal(err)
				}
			}
			parts := nativePointMigrationSections(t, "00072_order_merchant_intents.sql")
			_, err := f.db.Pool.Exec(f.ctx, parts[1])
			merchantJournalSQLState(t, err, "P0001")
			var exists bool
			if err = f.db.Pool.QueryRow(f.ctx, `SELECT to_regclass('orders_merchant_intents') IS NOT NULL`).Scan(&exists); err != nil || !exists {
				t.Fatal("refused rollback removed the journal", err)
			}
			if history == "intent" {
				items, err := f.db.OrderMerchantIntents(f.ctx, intent.Scope, 10)
				if err != nil || len(items) != 1 || items[0].Intent.Digest != intent.Digest {
					t.Fatal("refused rollback altered evidence", err)
				}
			}
		})
	}
}

func TestMerchantIntentPostgresMigrationEmptyRollbackAndUpgrade(t *testing.T) {
	f := orderInventoryDBFixture(t)
	parts := nativePointMigrationSections(t, "00072_order_merchant_intents.sql")
	executeNativePointMigration(t, f, parts[1])
	executeNativePointMigration(t, f, parts[0])
	items, err := f.db.OrderMerchantIntents(f.ctx, f.config.Scope, 10)
	if err != nil || len(items) != 0 {
		t.Fatal("clean schema rollback and re-upgrade failed", err)
	}
}

func TestMerchantIntentPostgresPairedHandoffWaitsForCarrier(t *testing.T) {
	for _, completion := range []string{"blocked_before_queue", "terminal_after_queue"} {
		t.Run(completion, func(t *testing.T) {
			f, native := merchantJournalHandoffFixture(t)
			carrier := native
			carrier.Action = orders.ActionPostingShip
			carrier = merchantJournalIdentity(t, carrier, 22)
			for _, intent := range []orders.MerchantIntent{native, carrier} {
				if err := merchantJournalInsert(f.ctx, f.db.Pool, intent, "OBSERVED", nil); err != nil {
					t.Fatal(err)
				}
			}
			items, err := f.db.PendingMerchantIntents(f.ctx, native.Scope, 10)
			if err != nil || len(items) != 1 || items[0].Intent.ID != carrier.ID {
				t.Fatal("older native movement selected before its exact carrier peer", items, err)
			}
			command := orders.Command{Scope: native.Scope, PostingNumber: native.Correlation.PostingNumber, Correlation: native.Correlation, Action: native.Action, MerchantIntent: &native}
			if err = validateMerchantPeerOrder(f.ctx, f.db.Pool, command); !errors.Is(err, orders.ErrBlocked) {
				t.Fatal("dispatch guard borrowed out-of-order native authority", err)
			}
			merchantJournalUnrelatedPeers(t, f, native)
			if completion == "blocked_before_queue" {
				if err = f.db.BlockMerchantIntent(f.ctx, carrier.Scope, carrier.ID, "CARRIER_REQUIRES_REVIEW"); err != nil {
					t.Fatal(err)
				}
			} else {
				merchantJournalFinishCarrier(t, f, carrier)
			}
			items, err = f.db.PendingMerchantIntents(f.ctx, native.Scope, 1)
			if err != nil || len(items) != 1 || items[0].Intent.ID != native.ID {
				t.Fatal("resolved carrier peer or unrelated source starved the native intent", items, err)
			}
			if err = validateMerchantPeerOrder(f.ctx, f.db.Pool, command); err != nil {
				t.Fatal("foreign scope or different source revision held native dispatch", err)
			}
		})
	}
}

func merchantJournalHandoffFixture(t *testing.T) (orderInventoryFixture, orders.MerchantIntent) {
	t.Helper()
	f, native := merchantJournalFixture(t)
	native.Action, native.Scheme, native.EventKind = orders.ActionDemandCreate, "rFBS", "handoff_confirmed"
	merchantJournalCurrentPoint(t, &f, &native)
	return f, native
}

func merchantJournalUnrelatedPeers(t *testing.T, f orderInventoryFixture, native orders.MerchantIntent) {
	t.Helper()
	other := merchantJournalOtherScope(t, f)
	foreign := native
	foreign.Scope, foreign.ConfigRevision = other.config.Scope, other.config.Revision
	foreign.Action = orders.ActionPostingShip
	foreign = merchantJournalIdentity(t, foreign, 23)
	if err := merchantJournalInsert(f.ctx, f.db.Pool, foreign, "OBSERVED", nil); err != nil {
		t.Fatal(err)
	}
	separate := native
	separate.Action, separate.AfterRevision = orders.ActionPostingShip, "unrelated-carrier-source-revision"
	separate = merchantJournalIdentity(t, separate, 24)
	if err := merchantJournalInsert(f.ctx, f.db.Pool, separate, "OBSERVED", nil); err != nil {
		t.Fatal(err)
	}
	command := orders.Command{Scope: native.Scope, PostingNumber: native.Correlation.PostingNumber, Correlation: native.Correlation, Action: native.Action, MerchantIntent: &native}
	independent := native
	independent.AfterRevision = "another-native-source-revision"
	command.MerchantIntent = &independent
	if err := validateMerchantPeerOrder(f.ctx, f.db.Pool, command); err != nil {
		t.Fatal("different native source revision borrowed an unrelated carrier hold", err)
	}
}

func merchantJournalFinishCarrier(t *testing.T, f orderInventoryFixture, carrier orders.MerchantIntent) {
	t.Helper()
	merchantJournalCandidateOperation(t, f)
	if err := merchantJournalBindCandidate(f, carrier, "merchant-carrier-peer"); err != nil {
		t.Fatal(err)
	}
	items, err := f.db.PendingMerchantIntents(f.ctx, carrier.Scope, 10)
	if err != nil || len(items) != 0 {
		t.Fatal("queued carrier did not fence its posting", items, err)
	}
	now := time.Now()
	work, err := f.db.ClaimOrderWork(f.ctx, orders.ClaimRequest{WorkerID: "merchant-carrier-terminal", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil || work == nil || work.ID != "merchant-carrier-peer" {
		t.Fatal("carrier outbox work unavailable", err)
	}
	err = f.db.CompleteOrderWork(f.ctx, orders.Completion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: work.State, State: orders.StateBlocked, Code: "CARRIER_REQUIRES_REVIEW"})
	if err != nil {
		t.Fatal("terminal carrier work did not release its exact fence", err)
	}
}
