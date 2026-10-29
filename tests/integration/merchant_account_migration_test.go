//go:build integration

package integration

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/testpublication"
)

func TestMerchantAccountMigrationPreservesHistoryAndIsolation(t *testing.T) {
	db, _ := controlledFixture(t)
	store, err := identity.New(db.Pool, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	owner, initial, err := store.Register(ctx, "migration-owner@example.test", "long-password-owner", "Initial")
	if err != nil {
		t.Fatal(err)
	}
	other, otherAccount, err := store.Register(ctx, "migration-other@example.test", "long-password-owner", "Other")
	if err != nil {
		t.Fatal(err)
	}
	verifyTestEmail(t, store, owner.Email)
	verifyTestEmail(t, store, other.Email)
	owner, err = store.Authenticate(ctx, owner.Email, "long-password-owner")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateSession(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	target := testpublication.Tenant
	if err = store.ClaimLegacy(ctx, target, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.BindLegacyMerchant(ctx, owner.ID, target); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Session(ctx, token); !errors.Is(err, identity.ErrAuth) {
		t.Fatalf("migration did not invalidate old session: %v", err)
	}
	if tenant, err := store.MerchantTenant(ctx, owner.ID); err != nil || tenant != target {
		t.Fatalf("owner account = %q, %v", tenant, err)
	}
	if tenant, err := store.MerchantTenant(ctx, other.ID); err != nil || tenant != otherAccount.ID {
		t.Fatalf("other account changed = %q, %v", tenant, err)
	}
	if err = store.BindLegacyMerchant(ctx, other.ID, target); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("foreign owner adopted legacy account: %v", err)
	}
	var connections, mappings, oldWorkspace, audit int
	err = db.Pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM stock_connections WHERE tenant_id=$1),
	 (SELECT count(*) FROM product_mappings WHERE tenant_id=$1),
	 (SELECT count(*) FROM saas_workspaces WHERE id=$2),
	 (SELECT count(*) FROM saas_security_events WHERE workspace_id=$1 AND actor_user_id=$3 AND action='LOCAL_MERCHANT_MIGRATION')`, target, initial.ID, owner.ID).Scan(&connections, &mappings, &oldWorkspace, &audit)
	if err != nil || connections != 2 || mappings != 1 || oldWorkspace != 1 || audit != 1 {
		t.Fatalf("history changed: connections=%d mappings=%d old=%d audit=%d err=%v", connections, mappings, oldWorkspace, audit, err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO saas_merchant_accounts(user_id,tenant_id) VALUES($1,$2)`, other.ID, target); err == nil {
		t.Fatal("database accepted a duplicate merchant binding")
	}
}

func TestMerchantMigrationRefusesToOrphanBusinessData(t *testing.T) {
	db, _ := controlledFixture(t)
	store, err := identity.New(db.Pool, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	owner, initial, err := store.Register(ctx, "migration-conflict@example.test", "long-password-owner", "Initial")
	if err != nil {
		t.Fatal(err)
	}
	verifyTestEmail(t, store, owner.Email)
	if err = db.RegisterStockConnection(ctx, initial.ID, "ozon", "existing-business-account", "synthetic", false); err != nil {
		t.Fatal(err)
	}
	if err = store.ClaimLegacy(ctx, testpublication.Tenant, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.BindLegacyMerchant(ctx, owner.ID, testpublication.Tenant); err == nil {
		t.Fatal("migration orphaned a nonempty merchant account")
	}
	if tenant, err := store.MerchantTenant(ctx, owner.ID); err != nil || tenant != initial.ID {
		t.Fatalf("original account changed = %q, %v", tenant, err)
	}
}

func TestMerchantAccountBackfillLeavesAmbiguousOwnershipUnassigned(t *testing.T) {
	db, _ := controlledFixture(t)
	migration := func(direction string) (string, error) {
		target := "38"
		if direction == "up-to" {
			target = "39"
		}
		cmd := exec.Command("go", "run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", db.Pool.Config().ConnString(), direction, target)
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	if output, err := migration("down-to"); err != nil {
		t.Fatalf("rollback empty merchant bindings: %v %s", err, output)
	}
	ctx := context.Background()
	_, err := db.Pool.Exec(ctx, `
	 INSERT INTO saas_users(id,email,password_hash,email_verified_at) VALUES
	 ('u_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','backfill-a@example.test',decode(repeat('aa',60),'hex'),clock_timestamp()),
	 ('u_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','backfill-b@example.test',decode(repeat('bb',60),'hex'),clock_timestamp()),
	 ('u_cccccccccccccccccccccccccccccccc','backfill-c@example.test',decode(repeat('cc',60),'hex'),clock_timestamp()),
	 ('u_dddddddddddddddddddddddddddddddd','backfill-d@example.test',decode(repeat('dd',60),'hex'),clock_timestamp());
	 INSERT INTO saas_workspaces(id,name) VALUES ('single-owner','single'),('shared-owner','shared'),('other-owner','other');
	 UPDATE saas_workspaces SET legacy_unclaimed=false WHERE id='cross-e2e';
	 INSERT INTO saas_memberships(workspace_id,user_id,role) VALUES
	 ('cross-e2e','u_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','OWNER'),
	 ('other-owner','u_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','OWNER'),
	 ('shared-owner','u_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','OWNER'),
	 ('shared-owner','u_cccccccccccccccccccccccccccccccc','OWNER'),
	 ('single-owner','u_dddddddddddddddddddddddddddddddd','OWNER')`)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := migration("up-to"); err != nil {
		t.Fatalf("migrate merchant bindings: %v %s", err, output)
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM saas_merchant_accounts WHERE user_id='u_dddddddddddddddddddddddddddddddd' AND tenant_id='single-owner'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unambiguous account missing: count=%d err=%v", count, err)
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM saas_merchant_accounts`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ambiguous ownership was assigned: count=%d err=%v", count, err)
	}
	if output, err := migration("down-to"); err == nil || !strings.Contains(output, "refusing to discard merchant ownership bindings") {
		t.Fatal("rollback did not protect the migrated account binding")
	}
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM stock_connections WHERE tenant_id='cross-e2e'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("legacy connections were changed")
	}
}
