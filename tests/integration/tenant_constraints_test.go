//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/testpublication"
)

func TestTenantForeignKeysAndOutboxIdentity(t *testing.T) {
	db, _ := controlledFixture(t)
	ctx := context.Background()
	a := testpublication.Scope()
	bTenant, bOzon, bMoy := "synthetic-tenant-b", "synthetic-ozon-b", "synthetic-moy-b"
	if err := db.RegisterStockConnection(ctx, bTenant, "ozon", bOzon, "synthetic", false); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterStockConnection(ctx, bTenant, "moysklad", bMoy, "synthetic", false); err != nil {
		t.Fatal(err)
	}
	_, err := db.Pool.Exec(ctx, `INSERT INTO product_mappings(tenant_id,ozon_account,ozon_product_id,ozon_offer_id,moysklad_account,target_kind,target_id,version,provenance)
	 VALUES($1,$2,'foreign-product','foreign-offer',$3,'product','item',1,'synthetic')`, bTenant, a.OzonAccount, bMoy)
	if err == nil {
		t.Fatal("product mapping crossed account ownership")
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO warehouse_mappings(tenant_id,moysklad_account,ozon_account,moysklad_warehouse_id,ozon_warehouse_id,status,version,provenance)
	 VALUES($1,$2,$3,'source-warehouse','market-warehouse','active',1,'synthetic')`, bTenant, a.MoyskladAccount, bOzon)
	if err == nil {
		t.Fatal("warehouse mapping crossed account ownership")
	}
	opA, _, err := db.SubmitCatalogImport(ctx, a.Tenant, "ozon", a.OzonAccount, "tenant-a-import", "synthetic", domain.DefaultOzonImportLimits())
	if err != nil {
		t.Fatal(err)
	}
	opB, _, err := db.SubmitCatalogImport(ctx, bTenant, "ozon", bOzon, "tenant-b-import", "synthetic", domain.DefaultOzonImportLimits())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(ctx, `UPDATE outbox_tasks SET import_operation_id=$2,import_tenant_id=$3,import_account_id=$4 WHERE import_operation_id=$1`, opA.ID, opB.ID, bTenant, bOzon)
	if err == nil {
		t.Fatal("outbox task identity was changed")
	}
	claim, err := db.ClaimForCatalogWorker(ctx, time.Minute, a.OzonAccount, "")
	if err != nil || claim == nil || claim.ImportOperationID != opA.ID {
		t.Fatalf("claim scoped import: %+v %v", claim, err)
	}
	forged := *claim
	forged.ImportOperationID = opB.ID
	if _, err = db.ImportForClaim(ctx, forged); !errors.Is(err, postgres.ErrImportClaimLost) {
		t.Fatalf("claim switched operations: %v", err)
	}
}

func TestImportRejectsCredentialRotationDuringRead(t *testing.T) {
	db, _ := controlledFixture(t)
	ctx := context.Background()
	scope := testpublication.Scope()
	credentialScope := credentials.Scope{Tenant: scope.Tenant, Provider: "ozon", Account: scope.OzonAccount}
	vault, err := credentials.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	for revision := int64(1); revision <= 2; revision++ {
		ciphertext, err := vault.Encrypt(credentialScope, revision, "synthetic-provider-key")
		if err != nil {
			t.Fatal(err)
		}
		digest := vault.Digest([]byte{byte(revision)})
		if _, _, err = db.SaveCredential(ctx, credentialScope, "Тест", revision-1, string(rune('a'+revision)), digest, ciphertext, time.Now(), "synthetic"); err != nil {
			t.Fatal(err)
		}
		if revision == 1 {
			_, _, err = db.SubmitCatalogImport(ctx, scope.Tenant, "ozon", scope.OzonAccount, "rotation-import", "synthetic", domain.DefaultOzonImportLimits())
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	claim, err := db.ClaimForCatalogWorker(ctx, time.Minute, scope.OzonAccount, "")
	if err != nil || claim == nil {
		t.Fatalf("claim import: %v", err)
	}
	now := time.Now().UTC()
	snapshot := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now}
	err = db.CommitClaimedOzonImportWithRevision(ctx, *claim, snapshot, "synthetic-rotated", domain.ImportCounts{}, 5*time.Second, 1)
	if !errors.Is(err, postgres.ErrImportCredentialChanged) {
		t.Fatalf("stale credential traversal was persisted: %v", err)
	}
	if err = db.FailImport(ctx, *claim, "CONFIGURATION_UNAVAILABLE", "", domain.ImportCounts{}); err != nil {
		t.Fatal(err)
	}
	op, err := db.GetCatalogOperation(ctx, scope.Tenant, "ozon", scope.OzonAccount, claim.ImportOperationID)
	if err != nil || op.BatchID != nil || op.Outcome == nil || *op.Outcome != "CONFIGURATION_UNAVAILABLE" {
		t.Fatalf("credential rotation outcome not durable: %+v %v", op, err)
	}
}

func TestPublicationAndCycleReferencesCannotCrossTenants(t *testing.T) {
	db, review := controlledFixture(t)
	ctx := context.Background()
	a := testpublication.Scope()
	b := domain.StockSyncScope{Tenant: "synthetic-reference-b", MoyskladAccount: "synthetic-reference-moy-b", OzonAccount: "synthetic-reference-ozon-b"}
	for _, item := range []struct{ provider, account string }{{"moysklad", b.MoyskladAccount}, {"ozon", b.OzonAccount}} {
		if err := db.RegisterStockConnection(ctx, b.Tenant, item.provider, item.account, "synthetic", false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO stock_sync_settings(tenant_id,moysklad_account,ozon_account) VALUES($1,$2,$3)`, b.Tenant, b.MoyskladAccount, b.OzonAccount); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE stock_sync_settings SET review_id=$4 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, b.Tenant, b.MoyskladAccount, b.OzonAccount, review.ID); err == nil {
		t.Fatal("foreign review attached to synchronization settings")
	}
	const foreignReview = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const foreignPublication = "cccccccccccccccccccccccccccccccc"
	if _, err := db.Pool.Exec(ctx, `INSERT INTO stock_sync_reviews(id,tenant_id,moysklad_account,ozon_account,ready,snapshot,created_at,expires_at) VALUES($1,$2,$3,$4,false,'{}',clock_timestamp(),clock_timestamp()+interval '1 minute')`, foreignReview, b.Tenant, b.MoyskladAccount, b.OzonAccount); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO stock_publications(id,tenant_id,moysklad_account,ozon_account,review_id) VALUES($1,$2,$3,$4,$5)`, foreignPublication, b.Tenant, b.MoyskladAccount, b.OzonAccount, review.ID); err == nil {
		t.Fatal("foreign review attached to publication")
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO stock_publications(id,tenant_id,moysklad_account,ozon_account,review_id) VALUES($1,$2,$3,$4,$5)`, foreignPublication, b.Tenant, b.MoyskladAccount, b.OzonAccount, foreignReview); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO stock_publication_targets(operation_id,ozon_account,product_id,warehouse_id,quantity,state,decision) VALUES($1,$2,'301','401','0','CANCELLED','{}')`, foreignPublication, b.OzonAccount); err != nil {
		t.Fatal(err)
	}
	publication := controlledEnable(t, db, review).LastOperation
	if _, err := db.Pool.Exec(ctx, `INSERT INTO stock_publication_targets(operation_id,ozon_account,product_id,warehouse_id,quantity,state,decision) VALUES($1,$2,'302','402','0','CANCELLED','{}')`, publication.ID, b.OzonAccount); err == nil {
		t.Fatal("foreign account attached to publication target")
	}
	foreignImport, _, err := db.SubmitCatalogImport(ctx, b.Tenant, "ozon", b.OzonAccount, "foreign-cycle-import", "synthetic", domain.DefaultOzonImportLimits())
	if err != nil {
		t.Fatal(err)
	}
	testCycleReferences(t, db, a, foreignImport.ID, foreignPublication, publication.ID)
	// Simulate a pre-migration invalid historical row in this disposable schema.
	// Such history must not become visible merely by claiming the workspace.
	if _, err = db.Pool.Exec(ctx, `ALTER TABLE stock_sync_settings DROP CONSTRAINT saas_settings_review_scope_fk`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE stock_sync_settings SET review_id=$4 WHERE tenant_id=$1 AND moysklad_account=$2 AND ozon_account=$3`, b.Tenant, b.MoyskladAccount, b.OzonAccount, review.ID); err != nil {
		t.Fatal(err)
	}
	store, err := identity.New(db.Pool, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := store.Register(ctx, "legacy-scope@example.test", "long-password-owner", "Synthetic")
	if err != nil {
		t.Fatal(err)
	}
	verifyTestEmail(t, store, user.Email)
	if err = store.ClaimLegacy(ctx, b.Tenant, user.ID); err == nil || !strings.Contains(err.Error(), "legacy publication links") {
		t.Fatalf("invalid historical links were exposed by a legacy claim: %v", err)
	}
}

func testCycleReferences(t *testing.T, db *postgres.Store, a domain.StockSyncScope, foreignImport, foreignPublication, publication string) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `INSERT INTO stock_auto_configs(tenant_id,moysklad_account,ozon_account,revision,enabled) VALUES($1,$2,$3,1,false)`, a.Tenant, a.MoyskladAccount, a.OzonAccount); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO stock_auto_config_audits(tenant_id,moysklad_account,ozon_account,revision,enabled,snapshot,principal_type,session_audit_id,idempotency_key,request_hash,created_at) VALUES($1,$2,$3,1,false,'{}','LOCAL_OPERATOR_SESSION','synthetic','synthetic-config-key','synthetic',clock_timestamp())`, a.Tenant, a.MoyskladAccount, a.OzonAccount); err != nil {
		t.Fatal(err)
	}
	const cycle = "dddddddddddddddddddddddddddddddd"
	if _, err = tx.Exec(ctx, `INSERT INTO stock_auto_cycles(id,tenant_id,moysklad_account,ozon_account,config_revision,state,started_at,next_action_at) VALUES($1,$2,$3,$4,1,'COMPLETE',clock_timestamp(),clock_timestamp())`, cycle, a.Tenant, a.MoyskladAccount, a.OzonAccount); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct{ sql, value string }{
		{`UPDATE stock_auto_cycles SET ozon_import_id=$2 WHERE id=$1`, foreignImport},
		{`UPDATE stock_auto_cycles SET publication_id=$2 WHERE id=$1`, foreignPublication},
		{`INSERT INTO stock_auto_cycle_publications(cycle_id,publication_id,product_id,warehouse_id) VALUES($1,$2,'301','401')`, foreignPublication},
	} {
		if _, err = db.Pool.Exec(ctx, statement.sql, cycle, statement.value); err == nil {
			t.Fatal("scheduler accepted a reference to another tenant")
		}
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO stock_auto_cycle_publications(cycle_id,publication_id,product_id,warehouse_id) VALUES($1,$2,$3,$4)`, cycle, publication, testpublication.OzonProduct, testpublication.OzonWarehouse); err != nil {
		t.Fatalf("same-tenant cycle publication was rejected: %v", err)
	}
}
