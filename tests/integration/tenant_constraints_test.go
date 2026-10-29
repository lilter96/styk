//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/domain"
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
