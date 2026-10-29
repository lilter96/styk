//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/identity"
)

func TestCatalogSyncMerchantActorRevalidatedAndSubstitutionBlocked(t *testing.T) {
	tests := []struct {
		name   string
		change string
	}{{"revoked actor", "revoked"}, {"substituted actor", "substituted"}, {"expired actor", "expired"}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := catalogSyncDB(t)
			session, tenant := expiringMerchantSession(t, db)
			if err := db.RegisterStockConnection(context.Background(), tenant, "moysklad", "actor-moy", "synthetic", false); err != nil {
				t.Fatal(err)
			}
			c := catalogsync.Config{Scope: catalog.Scope{Tenant: tenant, MoyskladAccount: "actor-moy", OzonAccount: "expiry-account"}, IntervalSeconds: 60, Targets: []catalogsync.Enrollment{}, Rules: catalog.Policies{}}
			actor := session.AuditID
			switch tc.change {
			case "revoked":
				if _, err := db.Pool.Exec(context.Background(), `UPDATE saas_sessions SET revoked_at=clock_timestamp() WHERE audit_id=$1`, session.AuditID); err != nil {
					t.Fatal(err)
				}
			case "expired":
				if _, err := db.Pool.Exec(context.Background(), `UPDATE saas_sessions SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE audit_id=$1`, session.AuditID); err != nil {
					t.Fatal(err)
				}
			case "substituted":
				actor = "local-operator-spoof"
			}
			ctx := identity.WithSession(context.Background(), session)
			if _, err := db.SaveCatalogConfig(ctx, catalogsync.SaveConfigRequest{Config: c, Actor: actor, IdempotencyKey: "unauthorized"}, time.Now()); err == nil {
				t.Fatal("unauthorized actor saved configuration")
			}
			var count int
			if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM catalog_sync_configs`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("unauthorized configuration persisted: %d %v", count, err)
			}
		})
	}
}

func TestCatalogSyncMerchantActorExpiryAfterLockWait(t *testing.T) {
	db := catalogSyncDB(t)
	session, tenant := expiringMerchantSession(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := db.RegisterStockConnection(ctx, tenant, "moysklad", "actor-moy", "synthetic", false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE saas_sessions SET created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()+interval '1 second' WHERE audit_id=$1`, session.AuditID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	var pid int
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM saas_sessions WHERE audit_id=$1 FOR UPDATE`, session.AuditID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		config := catalogsync.Config{Scope: catalog.Scope{Tenant: tenant, MoyskladAccount: "actor-moy", OzonAccount: "expiry-account"}, IntervalSeconds: 60}
		_, saveErr := db.SaveCatalogConfig(identity.WithSession(ctx, session), catalogsync.SaveConfigRequest{Config: config, Actor: session.AuditID, IdempotencyKey: "expires-under-lock"}, time.Now())
		done <- saveErr
	}()
	waitForBlockedOperation(t, ctx, db, pid)
	waitForMerchantSessionExpiry(t, ctx, db, session.AuditID)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("expired actor passed after lock wait")
	}
}

func TestCatalogSyncTenantFKOutboxIdentityAndFrozenEvidence(t *testing.T) {
	db, c := catalogSyncFixture(t)
	p := catalogSyncPreview(t, db, c)
	catalogSyncApprove(t, db, p)
	ctx := context.Background()
	statements := []struct{ name, sql string }{{"outbox tenant", "UPDATE outbox_tasks SET catalog_tenant_id='other-tenant' WHERE task_type='catalog_sync'"}, {"outbox type", "UPDATE outbox_tasks SET task_type='mock' WHERE task_type='catalog_sync'"}, {"command", "UPDATE catalog_sync_commands SET command='{}'::jsonb"}, {"preview", "UPDATE catalog_sync_previews SET preview='{}'::jsonb"}, {"snapshot", "DELETE FROM catalog_sync_snapshots"}, {"mapping association", "UPDATE catalog_sync_associations SET mapping_version=2"}, {"snapshot account JSON substitution", `INSERT INTO catalog_sync_snapshots(tenant_id,provider,account_id,entity_kind,entity_id,revision,product,observed_at) SELECT tenant_id,provider,account_id,entity_kind,entity_id,revision,jsonb_set(product,'{identity,account,id}','"foreign-account"'),observed_at FROM catalog_sync_snapshots LIMIT 1`}}
	for _, tc := range statements {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Pool.Exec(ctx, tc.sql); err == nil {
				t.Fatal("immutable identity or evidence changed")
			}
		})
	}
	w := catalogSyncClaim(t, db)
	foreign := w.Scope
	foreign.Tenant = "other-tenant"
	if err := db.CompleteCatalogWork(ctx, catalogsync.Completion{Scope: foreign, WorkID: w.ID, ClaimToken: w.ClaimToken, ExpectedState: w.State, State: catalogsync.StateBlocked, Now: time.Now()}); !errors.Is(err, catalogsync.ErrNotFound) {
		t.Fatalf("foreign completion: %v", err)
	}
}
