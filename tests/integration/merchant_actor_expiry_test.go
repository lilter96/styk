//go:build integration

package integration

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/postgres"
)

func TestMerchantActorExpiryIsCheckedAfterLockWait(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "current_migration"
		if rollback {
			name = "security_fix_retained_on_rollback"
		}
		t.Run(name, func(t *testing.T) {
			db, _ := controlledFixture(t)
			if rollback {
				cmd := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), "down-to", "41")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("rollback compatible security migration: %v %s", err, out)
				}
			}
			assertExpiredActorCannotCreateImport(t, db)
		})
	}
}

func expiringMerchantSession(t *testing.T, db *postgres.Store) (identity.Session, string) {
	t.Helper()
	ctx := context.Background()
	store, err := identity.New(db.Pool, db.Timeout)
	if err != nil {
		t.Fatal(err)
	}
	user, account, err := store.Register(ctx, "expiry-lock@example.test", "synthetic-password-123", "Synthetic")
	if err != nil {
		t.Fatal(err)
	}
	verifyTestEmail(t, store, user.Email)
	user, err = store.Authenticate(ctx, user.Email, "long-password-owner")
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := store.CreateSession(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.RegisterStockConnection(ctx, account.ID, "ozon", "expiry-account", "synthetic", false); err != nil {
		t.Fatal(err)
	}
	return session, account.ID
}

func assertExpiredActorCannotCreateImport(t *testing.T, db *postgres.Store) {
	t.Helper()
	session, tenant := expiringMerchantSession(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := db.Pool.Exec(ctx, `UPDATE saas_sessions SET created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()+interval '1 second' WHERE audit_id=$1`, session.AuditID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	var pid int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM saas_sessions WHERE audit_id=$1 FOR UPDATE`, session.AuditID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := db.SubmitCatalogImport(identity.WithSession(ctx, session), tenant, "ozon", "expiry-account", "expired-actor-import", session.AuditID, domain.DefaultOzonImportLimits())
		done <- err
	}()
	waitForBlockedOperation(t, ctx, db, pid)
	waitForMerchantSessionExpiry(t, ctx, db, session.AuditID)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var pgerr *pgconn.PgError
	if err := <-done; !errors.As(err, &pgerr) || pgerr.Code != "42501" {
		t.Fatalf("expired actor was not rejected after lock release: %v", err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM ozon_import_operations WHERE tenant_id=$1`, tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unauthorized import persisted: count=%d error=%v", count, err)
	}
}

func waitForMerchantSessionExpiry(t *testing.T, ctx context.Context, db *postgres.Store, auditID string) {
	t.Helper()
	for {
		var expired bool
		if err := db.Pool.QueryRow(ctx, `SELECT expires_at<=clock_timestamp() FROM saas_sessions WHERE audit_id=$1`, auditID).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("merchant session did not expire")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
