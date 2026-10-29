//go:build integration

package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orderinventory"
	"github.com/lilter96/styk/internal/orders"
)

func orderInventoryTestDB(t *testing.T) *Store {
	t.Helper()
	for _, key := range []string{"MOYSKLAD_TOKEN", "MOYSKLAD_ACCOUNT_ID", "OZON_API_KEY", "OZON_CLIENT_ID"} {
		if os.Getenv(key) != "" {
			t.Fatal("inventory attribution tests require blank live provider environment")
		}
	}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		raw, err := os.ReadFile("../../.local/v7/test-db-dsn")
		if err != nil {
			t.Fatal("dedicated inventory attribution test database unavailable")
		}
		dsn = strings.TrimSpace(string(raw))
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "postgres" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || (u.Path != "/styk_orders_store_test" && u.Path != "/styk_orders_release_test") {
		t.Fatal("inventory attribution tests require dedicated localhost orders test database")
	}
	return orderInventoryIsolatedSchema(t, u)
}

func orderInventoryIsolatedSchema(t *testing.T, u *url.URL) *Store {
	t.Helper()
	admin, err := Open(context.Background(), u.String(), 15*time.Second)
	if err != nil {
		t.Fatal("open inventory attribution test database")
	}
	schema := fmt.Sprintf("order_inventory_%d", time.Now().UnixNano())
	if _, err = admin.Pool.Exec(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal("create inventory attribution isolated schema")
	}
	t.Cleanup(func() {
		if _, err := admin.Pool.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error("drop inventory attribution isolated schema")
		}
		admin.Pool.Close()
	})
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	orderInventoryMigrate(t, u.String())
	db, err := Open(context.Background(), u.String(), 15*time.Second)
	if err != nil {
		t.Fatal("open inventory attribution isolated schema")
	}
	t.Cleanup(db.Pool.Close)
	return db
}

func orderInventoryMigrate(t *testing.T, dsn string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args := []string{"run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", dsn, "up"}
	if output, err := exec.CommandContext(ctx, "go", args...).CombinedOutput(); err != nil {
		t.Fatalf("inventory attribution test migration: %v %s", err, output)
	}
}

type orderInventoryFixture struct {
	db      *Store
	config  orders.Config
	ctx     context.Context
	now     time.Time
	request domain.StockCalculationRequest
}

func orderInventoryDBFixture(t *testing.T) orderInventoryFixture {
	t.Helper()
	db, now := orderInventoryTestDB(t), time.Now().UTC().Truncate(time.Microsecond)
	scope := orders.Scope{Tenant: "inventory-tenant", MoyskladAccount: "inventory-moy", OzonAccount: "inventory-ozon"}
	actor := orderInventoryOwner(t, db, scope.Tenant)
	ctx := identity.WithSession(context.Background(), actor)
	for _, connection := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err := db.RegisterStockConnection(context.Background(), scope.Tenant, connection.provider, connection.account, "synthetic inventory attribution", true); err != nil {
			t.Fatal(err)
		}
		inventoryExec(t, db, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,ciphertext,checked_at,last_request_id) VALUES($1,$2,$3,'Synthetic',1,$4,clock_timestamp(),'inventory-fixture')`, scope.Tenant, connection.provider, connection.account, make([]byte, 29))
	}
	orderInventoryMappings(t, db, scope, now)
	config := orders.Config{Scope: scope, IntervalSeconds: 300, SourceFreshnessSeconds: 300, PreviewTTLSeconds: 300, OrganizationID: "organization", AgentID: "agent", StoreID: "native-store", StateID: "state", SalesChannelID: "channel"}
	config, err := db.SaveOrderConfig(ctx, orders.SaveConfigRequest{Config: config, Actor: actor.AuditID, IdempotencyKey: "inventory-config"}, now)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.StockCalculationRequest{Pool: domain.StockPool{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, WarehouseID: "native-store", ItemKind: "product", ItemID: "native-product"}, Target: domain.StockTarget{OzonAccount: scope.OzonAccount, WarehouseID: "1002", ProductID: "1001"}, Policy: domain.StockCalculationPolicy{Version: 1, Unit: "piece", SafetyStock: "0", MaxObservationAge: 15 * time.Minute, DecisionLifetime: time.Minute}}
	return orderInventoryFixture{db: db, config: config, ctx: ctx, now: now, request: request}
}

func inventoryExec(t *testing.T, db *Store, sql string, args ...any) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func orderInventoryOwner(t *testing.T, db *Store, tenant string) identity.Session {
	t.Helper()
	owner, audit := "u_"+strings.Repeat("1", 32), "user."+strings.Repeat("a", 22)
	inventoryExec(t, db, `INSERT INTO saas_users(id,email,password_hash,email_verified_at) VALUES($1,'inventory@example.test',$2,clock_timestamp())`, owner, make([]byte, 40))
	inventoryExec(t, db, `INSERT INTO saas_workspaces(id,name) VALUES($1,'Inventory attribution fixture')`, tenant)
	inventoryExec(t, db, `INSERT INTO saas_memberships(workspace_id,user_id,role) VALUES($1,$2,'OWNER')`, tenant, owner)
	inventoryExec(t, db, `INSERT INTO saas_merchant_accounts(user_id,tenant_id) VALUES($1,$2)`, owner, tenant)
	inventoryExec(t, db, `INSERT INTO saas_sessions(audit_id,token_hash,csrf_token,user_id,user_auth_version,expires_at) VALUES($1,$2,$3,$4,1,clock_timestamp()+interval '1 hour')`, audit, make([]byte, 32), strings.Repeat("c", 43), owner)
	return identity.Session{AuditID: audit, User: identity.User{ID: owner, AuthVersion: 1}}
}

func orderInventoryMappings(t *testing.T, db *Store, scope orders.Scope, now time.Time) {
	t.Helper()
	ctx := context.Background()
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now.Add(-10 * time.Minute), FinishedAt: now.Add(-9 * time.Minute), Entities: []domain.ExternalEntity{{Kind: "product", ID: "native-product", Article: "inventory-offer"}, {Kind: "store", ID: "native-store", Name: "Synthetic store"}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: source.StartedAt, FinishedAt: source.FinishedAt, Products: []domain.OzonProduct{{ProductID: "1001", OfferID: "inventory-offer", SKU: "2001"}}, Warehouses: []domain.OzonWarehouse{{ID: "1002", Name: "Synthetic FBS warehouse", IsRFBS: inventoryPtr(false)}}}
	if _, err := db.SaveInventoryImport(ctx, source, "inventory-source-original"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "inventory-market-original"); err != nil {
		t.Fatal(err)
	}
	match := MatchScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}
	if _, err := db.ConfirmMatch(ctx, match, "1001", "product", "native-product", "synthetic inventory mapping"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ConfirmWarehouseMapping(ctx, match, "native-store", "1002", "synthetic inventory mapping"); err != nil {
		t.Fatal(err)
	}
}

func inventoryPtr[T any](value T) *T { return &value }

func orderInventoryPosting(t *testing.T, fixture orderInventoryFixture, number, status string) orders.Posting {
	t.Helper()
	posting := orders.Posting{Scope: fixture.config.Scope, CommercialOrderID: "commercial", PostingNumber: number, Status: orders.Present(status), Scheme: orders.Present("FBS"), WarehouseID: orders.Present("1002"), Lines: []orders.PostingLine{{Identity: orders.LineIdentity{OfferID: "inventory-offer", SKU: "2001"}, ProductID: orders.Present("1001"), Quantity: "3"}}, Evidence: orders.EvidenceRevision{Revision: "source-" + number, Origin: orders.OriginOzon, ObservedAt: fixture.now.Add(-6 * time.Minute)}}
	tx, err := fixture.db.Pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	if _, err = insertOrderPosting(fixture.ctx, tx, posting, fixture.now.Add(-6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	return posting
}

func orderInventoryBaseline(t *testing.T, fixture orderInventoryFixture, fingerprint, stock, reserve string) int64 {
	t.Helper()
	source := domain.InventoryImport{Account: fixture.config.Scope.MoyskladAccount, StartedAt: fixture.now.Add(-2 * time.Minute), FinishedAt: fixture.now.Add(-time.Minute), WarehouseStock: []domain.WarehouseStockObservation{{Kind: "product", ExternalID: "native-product", WarehouseID: "native-store", Stock: &stock, Reserve: &reserve, InTransit: inventoryPtr("0")}}}
	if _, err := fixture.db.SaveInventoryImport(context.Background(), source, fingerprint); err != nil {
		t.Fatal(err)
	}
	var importID int64
	if err := fixture.db.Pool.QueryRow(context.Background(), `SELECT max(id) FROM source_imports WHERE source='moysklad' AND account_id=$1`, source.Account).Scan(&importID); err != nil {
		t.Fatal(err)
	}
	return importID
}

func TestOrderInventoryPostgresPendingAndHistoricalCancellation(t *testing.T) {
	fixture := orderInventoryDBFixture(t)
	orderInventoryPosting(t, fixture, "historical", "cancelled")
	baseline := orderInventoryBaseline(t, fixture, "baseline-original", "10", "3")
	guard, err := fixture.db.OrderInventoryGuard(fixture.ctx, fixture.request, baseline, fixture.now)
	if err != nil || guard.Blocked || len(guard.Obligations) != 1 || guard.Obligations[0].State != orderinventory.Cancelled {
		t.Fatalf("unprojected historical cancellation blocked unrelated source reserve: %+v %v", guard, err)
	}
	orderInventoryPosting(t, fixture, "active", "awaiting_packaging")
	guard, err = fixture.db.OrderInventoryGuard(fixture.ctx, fixture.request, baseline, fixture.now)
	if err != nil || !guard.Blocked || len(guard.Obligations) != 2 {
		t.Fatalf("unreserved order did not block physical pool: %+v %v", guard, err)
	}
	request := fixture.request
	request.Pool.Tenant = "another-tenant"
	guard, err = fixture.db.OrderInventoryGuard(context.Background(), request, baseline, fixture.now)
	if err != nil || len(guard.Obligations) != 0 {
		t.Fatalf("order attribution crossed tenant scope: %+v %v", guard, err)
	}
}

func inventoryJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := ordersJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func orderInventoryReconciled(t *testing.T, fixture orderInventoryFixture, posting orders.Posting, reserve string) orders.Correlation {
	t.Helper()
	bindings, err := readOrderCorrelations(fixture.ctx, fixture.db.Pool, posting.Scope)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("read synthetic binding: %v", err)
	}
	binding := bindings[0]
	binding.CustomerOrderID = "native-order"
	command := orders.Command{ID: "native-command", Scope: posting.Scope, Action: orders.ActionMirrorCreate, PostingNumber: posting.PostingNumber, Correlation: bindings[0], ExpectedSourceRevision: posting.Evidence.Revision}
	work := orders.Work{ID: "native-work", OperationID: "native-operation", Scope: posting.Scope, ConfigRevision: fixture.config.Revision, State: orders.StateReconciled, Command: command, CommandFingerprint: strings.Repeat("a", 64)}
	target := orders.TargetObservation{Scope: posting.Scope, Correlation: binding, Revision: "native-revision", Complete: true, ObservedAt: fixture.now.Add(-5 * time.Minute), Fields: orders.CustomerOrderWrite{StoreID: fixture.config.StoreID, Applicable: orders.Present(true)}, Positions: []orders.TargetPosition{{ID: "native-position", MoyskladKind: "product", MoyskladID: "native-product", Quantity: "3", Reserve: orders.Present(orders.Decimal(reserve))}}}
	tx, err := fixture.db.Pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	orderInventorySeedWork(t, fixture, tx, work, target)
	if err = resolveOrderProjection(fixture.ctx, tx, binding, fixture.now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	return binding
}

func orderInventorySeedWork(t *testing.T, fixture orderInventoryFixture, tx pgx.Tx, work orders.Work, target orders.TargetObservation) {
	t.Helper()
	scope := fixture.config.Scope
	preview := orders.Preview{ID: "native-preview", Scope: scope, ConfigRevision: fixture.config.Revision}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(fixture.ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO orders_previews(id,tenant_id,moysklad_account,ozon_account,config_revision,preview,fingerprint,actor,automatic,owner_user_id,created_at,expires_at) SELECT $1,tenant_id,moysklad_account,ozon_account,revision,$2,$3,actor,false,owner_user_id,$4,$5 FROM orders_config_revisions WHERE tenant_id=$6`, preview.ID, inventoryJSON(t, preview), strings.Repeat("a", 64), fixture.now.Add(-6*time.Minute), fixture.now.Add(time.Minute), scope.Tenant)
	exec(`INSERT INTO orders_operations(id,tenant_id,moysklad_account,ozon_account,config_revision,preview_id,actor,owner_user_id,automatic,request_key,request_hash,created_at) SELECT $1,tenant_id,moysklad_account,ozon_account,revision,$2,actor,owner_user_id,false,'native-request',$3,$4 FROM orders_config_revisions WHERE tenant_id=$5`, work.OperationID, preview.ID, strings.Repeat("a", 64), fixture.now.Add(-5*time.Minute), scope.Tenant)
	exec(`INSERT INTO orders_work(id,operation_id,tenant_id,moysklad_account,ozon_account,config_revision,posting_number,command_id,command_fingerprint,action,state,work,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'mirror_create','RECONCILED',$10,$11,$11)`, work.ID, work.OperationID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, work.ConfigRevision, work.Command.PostingNumber, work.Command.ID, work.CommandFingerprint, inventoryJSON(t, work), fixture.now.Add(-5*time.Minute))
	exec(`INSERT INTO outbox_tasks(operation_key,task_type,state,orders_operation_id,orders_work_id,orders_tenant_id,orders_moysklad_account,orders_ozon_account,max_attempts) VALUES('native-reconciliation','orders_sync','succeeded',$1,$2,$3,$4,$5,1)`, work.OperationID, work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount)
	exec(`INSERT INTO orders_reconciliations(work_id,tenant_id,moysklad_account,ozon_account,correlation,observed_target,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, work.ID, scope.Tenant, scope.MoyskladAccount, scope.OzonAccount, inventoryJSON(t, target.Correlation), inventoryJSON(t, target), target.ObservedAt)
}

func orderInventoryNativeBatch(t *testing.T, fixture orderInventoryFixture, binding orders.Correlation, reserve, shipped string) {
	t.Helper()
	batch := domain.CustomerOrderImport{Tenant: fixture.config.Scope.Tenant, Account: fixture.config.Scope.MoyskladAccount, Since: fixture.now.Add(-time.Hour), Until: fixture.now, StartedAt: fixture.now.Add(-4 * time.Minute), FinishedAt: fixture.now.Add(-3 * time.Minute)}
	batch.Orders = []domain.CustomerOrderObservation{{ID: binding.CustomerOrderID, Account: batch.Account, SyncID: &binding.SyncID, WarehouseID: inventoryPtr(fixture.config.StoreID), Applicable: inventoryPtr(true), ObservedAt: batch.FinishedAt, Positions: []domain.CustomerOrderPosition{{ID: "native-position", AssortmentKind: "product", AssortmentID: "native-product", Quantity: inventoryPtr("3"), Reserve: &reserve, Shipped: &shipped, ObservedAt: batch.FinishedAt}}}}
	if _, err := fixture.db.SaveCustomerOrders(fixture.ctx, batch); err != nil {
		t.Fatal(err)
	}
}

func TestOrderInventoryPostgresNativeReserveAttribution(t *testing.T) {
	for _, tc := range []struct {
		name, status, reserve, shipped, physical, baselineReserve string
		blocked                                                   bool
		state                                                     orderinventory.State
	}{
		{"matched_once", "awaiting_packaging", "3", "0", "10", "3", false, orderinventory.BaselineMatched},
		{"foreign_reserve_cannot_cover_zero", "awaiting_packaging", "0", "0", "10", "3", true, orderinventory.Unreflected},
		{"cancelled_preserves_foreign_reserve", "cancelled", "0", "0", "10", "3", false, orderinventory.Cancelled},
		{"shipped_uses_reduced_physical", "delivered", "0", "3", "7", "2", false, orderinventory.Shipped},
		{"aggregate_does_not_cover_native", "awaiting_packaging", "3", "0", "10", "2", true, orderinventory.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := orderInventoryDBFixture(t)
			posting := orderInventoryPosting(t, fixture, "posting", tc.status)
			binding := orderInventoryReconciled(t, fixture, posting, tc.reserve)
			baseline := orderInventoryBaseline(t, fixture, "baseline-selected", tc.physical, tc.baselineReserve)
			guard, err := fixture.db.OrderInventoryGuard(fixture.ctx, fixture.request, baseline, fixture.now)
			if err != nil || guard.Blocked != (tc.status != "cancelled" || tc.reserve != "0") {
				t.Fatalf("binding/readback alone proved baseline coverage: %+v %v", guard, err)
			}
			orderInventoryNativeBatch(t, fixture, binding, tc.reserve, tc.shipped)
			guard, err = fixture.db.OrderInventoryGuard(fixture.ctx, fixture.request, baseline, fixture.now)
			if err != nil || guard.Blocked != tc.blocked || len(guard.Obligations) != 1 || guard.Obligations[0].State != tc.state {
				t.Fatalf("native attribution: %+v %v", guard, err)
			}
		})
	}
}

func TestOrderInventoryPostgresSharedPoolIncludesOtherSeller(t *testing.T) {
	fixture := orderInventoryDBFixture(t)
	other := fixture
	other.config.Scope.OzonAccount = "inventory-other-ozon"
	scope := other.config.Scope
	if err := fixture.db.RegisterStockConnection(context.Background(), scope.Tenant, "ozon", scope.OzonAccount, "synthetic other seller", true); err != nil {
		t.Fatal(err)
	}
	inventoryExec(t, fixture.db, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,ciphertext,checked_at,last_request_id) VALUES($1,'ozon',$2,'Synthetic',1,$3,clock_timestamp(),'other-seller')`, scope.Tenant, scope.OzonAccount, make([]byte, 29))
	orderInventoryMappings(t, fixture.db, scope, fixture.now)
	actor, ok := identity.SessionFromContext(fixture.ctx)
	if !ok {
		t.Fatal("synthetic owner session missing")
	}
	config, err := fixture.db.SaveOrderConfig(fixture.ctx, orders.SaveConfigRequest{Config: other.config, Actor: actor.AuditID, IdempotencyKey: "other-seller-config"}, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	other.config = config
	orderInventoryPosting(t, other, "other-seller-active", "awaiting_packaging")
	baseline := orderInventoryBaseline(t, fixture, "baseline-shared", "10", "3")
	guard, err := fixture.db.OrderInventoryGuard(fixture.ctx, fixture.request, baseline, fixture.now)
	if err != nil || !guard.Blocked || len(guard.Obligations) != 1 || guard.Obligations[0].Scope.OzonAccount != scope.OzonAccount {
		t.Fatalf("publication for first seller ignored second seller's pool obligation: %+v %v", guard, err)
	}
}
