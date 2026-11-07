//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orderflow"
	"github.com/lilter96/styk/internal/orders"
)

type sourceRefreshReader struct{ calls int }

func (r *sourceRefreshReader) ReadOrderPage(context.Context, orders.Scope, orders.OrderWindow, string) (orders.OrderSourcePage, error) {
	r.calls++
	return orders.OrderSourcePage{Complete: true, Exhausted: true}, nil
}
func (*sourceRefreshReader) ReadOrderPosting(context.Context, orders.Scope, string) (orders.Posting, error) {
	return orders.Posting{}, orders.ErrNotFound
}

func sourceRefreshScope(t *testing.T) ordersStoreFixture {
	t.Helper()
	f := newOrdersStoreFixture(t)
	f.config.Scope.OzonAccount = "900999"
	if err := f.db.RegisterStockConnection(f.ctx, f.config.Scope.Tenant, "ozon", f.config.Scope.OzonAccount, "connected source fixture", true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(f.ctx, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,ciphertext,checked_at,last_request_id) VALUES($1,'ozon',$2,'Synthetic',1,$3,clock_timestamp(),'connected-source')`, f.config.Scope.Tenant, f.config.Scope.OzonAccount, make([]byte, 29)); err != nil {
		t.Fatal(err)
	}
	return f
}

func submitSourceRefresh(t *testing.T, f ordersStoreFixture) domain.ImportOperation {
	t.Helper()
	op, replay, err := f.db.SubmitOzonRefresh(f.ctx, f.config.Scope, "connected-source-refresh", f.session.AuditID, domain.DefaultOzonImportLimits())
	if err != nil || replay || op.OrdersRefresh == nil || op.OrdersRefresh.State != "QUEUED" {
		t.Fatal("durable source refresh not queued", err)
	}
	return op
}

func TestOrdersSourceRefreshLoadsThroughIngestorWithoutTargetPermission(t *testing.T) {
	f := sourceRefreshScope(t)
	op := submitSourceRefresh(t, f)
	config, err := f.db.OrderConfig(f.ctx, f.config.Scope)
	if err != nil || !config.ReadEnabled || config.Enabled || config.TestEnabled || config.Automation != nil || config.Revision != 1 {
		t.Fatal("source refresh granted target writes", err)
	}
	source := &sourceRefreshReader{}
	collector, err := orderflow.NewIngestor(f.db, source, orderflow.IngestionOptions{WorkerID: "source-refresh-test"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = collector.Collect(context.Background(), config.Scope); err != nil {
			t.Fatal(err)
		}
		if err = f.db.RecordOrderSourceRefresh(f.ctx, config.Scope, nil); err != nil {
			t.Fatal(err)
		}
	}
	current, err := f.db.GetCatalogOperation(f.ctx, config.Scope.Tenant, "ozon", config.Scope.OzonAccount, op.ID)
	if err != nil || current.OrdersRefresh.State != "SUCCEEDED" || source.calls != 2 {
		t.Fatal("durable checkpoint progress not reported", err)
	}
	var writes int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_work`).Scan(&writes); err != nil || writes != 0 {
		t.Fatal("readonly refresh created provider write", err)
	}
}

func TestOrdersSourceRefreshReplayPreservesRevocationAndTargetRevision(t *testing.T) {
	f := newOrdersStoreFixture(t)
	op := submitSourceRefresh(t, f)
	current, err := f.db.OrderConfig(f.ctx, f.config.Scope)
	if err != nil || current.Revision != f.config.Revision || current.Enabled != f.config.Enabled {
		t.Fatal("refresh rewrote target policy", err)
	}
	disabled := false
	current, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: current, ReadEnabled: &disabled, ExpectedRevision: current.Revision, Actor: f.session.AuditID, IdempotencyKey: "disable-source-refresh"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if err = f.db.RequestOrderCheck(f.ctx, current.Scope, f.session.AuditID); !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("explicit source revocation did not stop check requests", err)
	}
	due, dueErr := f.db.DueOrderConfigs(f.ctx, time.Now(), 25)
	if dueErr != nil || len(due) != 0 {
		t.Fatal("explicit source revocation did not stop scheduler", dueErr)
	}
	if _, err = f.db.Pool.Exec(f.ctx, `UPDATE connection_credentials SET enabled=false,ciphertext=NULL,revision=revision+1 WHERE tenant_id=$1`, f.config.Scope.Tenant); err != nil {
		t.Fatal(err)
	}
	replay, exists, err := f.db.SubmitOzonRefresh(f.ctx, f.config.Scope, "connected-source-refresh", f.session.AuditID, domain.DefaultOzonImportLimits())
	if err != nil || !exists || replay.ID != op.ID || replay.OrdersRefresh.State != "CANCELLED" {
		t.Fatal("refresh replay did not retain cancellation", err)
	}
	after, err := f.db.OrderConfig(f.ctx, f.config.Scope)
	if err != nil || after.ReadEnabled || after.Revision != current.Revision {
		t.Fatal("replay re-enabled permission or changed revision", err)
	}
}

func TestOrdersSourceRefreshFailureAndForeignScopeAreNotSuccessful(t *testing.T) {
	f := sourceRefreshScope(t)
	op := submitSourceRefresh(t, f)
	if err := f.db.RecordOrderSourceRefresh(f.ctx, f.config.Scope, errors.New("synthetic provider failure")); err != nil {
		t.Fatal(err)
	}
	current, err := f.db.GetCatalogOperation(f.ctx, f.config.Scope.Tenant, "ozon", f.config.Scope.OzonAccount, op.ID)
	if err != nil || current.OrdersRefresh.State != "FAILED" || current.OrdersRefresh.Code != "SOURCE_READ_FAILED" {
		t.Fatal("failed source read presented as success", err)
	}
	foreign := f.config.Scope
	foreign.MoyskladAccount = "foreign-moy"
	if _, _, err = f.db.SubmitOzonRefresh(f.ctx, foreign, "foreign-source-refresh", f.session.AuditID, domain.DefaultOzonImportLimits()); err == nil {
		t.Fatal("foreign account accepted")
	}
	var count int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM ozon_import_operations`).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected request leaked a catalog task", err)
	}
}

func TestOrdersSourceRefreshMigrationRollbackPreservesHistory(t *testing.T) {
	f := sourceRefreshScope(t)
	submitSourceRefresh(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := migrationCommand(t, ctx, "-dir", "../../migrations", "postgres", f.db.Pool.Config().ConnString(), "down-to", "67")
	if _, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("rollback discarded source permission and refresh audit")
	}
	config, err := f.db.OrderConfig(f.ctx, f.config.Scope)
	if err != nil || !config.ReadEnabled {
		t.Fatal("refused rollback damaged source permission", err)
	}
}

func TestOrdersSourceRefreshConfigReplayReflectsCurrentPermission(t *testing.T) {
	f := sourceRefreshScope(t)
	submitSourceRefresh(t, f)
	config, err := f.db.OrderConfig(f.ctx, f.config.Scope)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	request := orders.SaveConfigRequest{Config: config, ReadEnabled: &enabled, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "source-config-save"}
	saved, err := f.db.SaveOrderConfig(f.ctx, request, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.db.SaveOrderConfig(f.ctx, request, time.Now())
	if err != nil || !replay.ReadEnabled || !replay.ReadConfigured || replay.Revision != saved.Revision {
		t.Fatal("config replay lost source permission", err)
	}
	disabled := false
	saved, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: saved, ReadEnabled: &disabled, ExpectedRevision: saved.Revision, Actor: f.session.AuditID, IdempotencyKey: "source-config-revoke"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	replay, err = f.db.SaveOrderConfig(f.ctx, request, time.Now())
	if err != nil || replay.ReadEnabled || !replay.ReadConfigured {
		t.Fatal("replay misrepresented current revoked permission", err)
	}
	current, err := f.db.OrderConfig(f.ctx, f.config.Scope)
	if err != nil || current.ReadEnabled || current.Revision != saved.Revision {
		t.Fatal("replay restored revoked permission", err)
	}
}

func TestOrdersSourceRefreshStillRequiresCurrentMerchantOwner(t *testing.T) {
	f := sourceRefreshScope(t)
	submitSourceRefresh(t, f)
	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE saas_users SET disabled_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	source := &sourceRefreshReader{}
	collector, err := orderflow.NewIngestor(f.db, source, orderflow.IngestionOptions{WorkerID: "revoked-source-reader"})
	if err != nil {
		t.Fatal(err)
	}
	if err = collector.Collect(context.Background(), f.config.Scope); !errors.Is(err, orders.ErrBlocked) || source.calls != 0 {
		t.Fatal("revoked merchant retained provider read access", err, source.calls)
	}
}
