//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/testpublication"
)

func catalogSyncDB(t testing.TB) *postgres.Store {
	t.Helper()
	for _, key := range []string{"MOYSKLAD_TOKEN", "MOYSKLAD_ACCOUNT_ID", "OZON_API_KEY", "OZON_CLIENT_ID"} {
		if os.Getenv(key) != "" {
			t.Fatal("real provider environment must be absent from catalog tests")
		}
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || !strings.Contains(u.Path, "_test") {
		t.Fatal("catalog tests require a dedicated localhost test database")
	}
	ctx := context.Background()
	admin, err := postgres.Open(ctx, u.String(), 15*time.Second)
	if err != nil {
		t.Fatal("open isolated catalog test database")
	}
	schema := fmt.Sprintf("catalog_sync_%d", time.Now().UnixNano())
	if _, err = admin.Pool.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, cleanupErr := admin.Pool.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); cleanupErr != nil {
			t.Error("drop catalog test schema")
		}
		admin.Pool.Close()
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	catalogSyncMigrate(t, u.String(), "up")
	db, err := postgres.Open(ctx, u.String(), 15*time.Second)
	if err != nil {
		t.Fatal("open catalog schema")
	}
	t.Cleanup(db.Pool.Close)
	return db
}

func catalogSyncMigrate(t testing.TB, dsn string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmdArgs := []string{"run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "../../migrations", "postgres", dsn}
	cmd := exec.CommandContext(ctx, "go", append(cmdArgs, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("catalog test migration failed: %v %s", err, output)
	}
}

func catalogSyncFixture(t testing.TB) (*postgres.Store, catalogsync.Config) {
	t.Helper()
	db := catalogSyncDB(t)
	scope := catalog.Scope{Tenant: testpublication.Tenant, MoyskladAccount: testpublication.MoyskladAccount, OzonAccount: testpublication.OzonAccount}
	catalogSyncConnections(t, db, scope)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "source-1", Name: "New name", Article: "shared-1"}, {Kind: "product", ID: "source-2", Name: "Second", Article: "shared-2"}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now.Add(-time.Second), FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "1001", OfferID: "shared-1", Name: "Old name"}, {ProductID: "1002", OfferID: "shared-2", Name: "Second old"}}}
	if _, err := db.SaveInventoryImport(ctx, source, "catalog-source-v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "catalog-market-v1"); err != nil {
		t.Fatal(err)
	}
	_, err := db.ConfirmMatch(ctx, postgres.MatchScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}, "1001", "product", "source-1", "catalog-test")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err = db.Pool.QueryRow(ctx, `SELECT id FROM product_mappings WHERE tenant_id=$1 AND ozon_product_id='1001'`, scope.Tenant).Scan(&id); err != nil {
		t.Fatal(err)
	}
	enrollment := catalogsync.Enrollment{ID: "enrolled-1", MappingID: strconv.FormatInt(id, 10), MappingVersion: 1, Source: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderMoySklad, ID: scope.MoyskladAccount}, EntityType: "product", ID: "source-1"}, Target: catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon, ID: scope.OzonAccount}, EntityType: "product", ID: "1001", OfferID: "shared-1"}}
	c := catalogsync.Config{Scope: scope, Enabled: true, TestEnabled: true, IntervalSeconds: 60, Rules: catalog.Policies{catalog.FieldName: catalog.MoySkladAuthoritative}, Targets: []catalogsync.Enrollment{enrollment}}
	c, err = db.SaveCatalogConfig(ctx, catalogsync.SaveConfigRequest{Config: c, Actor: "catalog-test-actor", IdempotencyKey: "config-v1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	return db, c
}

func catalogSyncConnections(t testing.TB, db *postgres.Store, scope catalog.Scope) {
	t.Helper()
	ctx := context.Background()
	for _, connection := range []struct{ provider, account string }{{"moysklad", scope.MoyskladAccount}, {"ozon", scope.OzonAccount}} {
		if err := db.RegisterStockConnection(ctx, scope.Tenant, connection.provider, connection.account, "catalog isolated test", true); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,enabled,ciphertext,checked_at,last_request_id) VALUES($1,$2,$3,'Synthetic',1,true,$4,clock_timestamp(),'synthetic-test')`, scope.Tenant, connection.provider, connection.account, make([]byte, 29)); err != nil {
			t.Fatal(err)
		}
	}
}

func catalogSyncPreview(t testing.TB, db *postgres.Store, c catalogsync.Config) catalogsync.Preview {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	review, err := db.CatalogReviewContext(ctx, c.Scope)
	if err != nil {
		t.Fatal(err)
	}
	target := review.Targets[0]
	source := catalog.Product{Identity: target.Enrollment.Source, Lifecycle: catalog.LifecycleActive, Fields: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("New name")}, Revision: "source-r1", ObservedAt: now}
	dest := catalog.Product{Identity: target.Enrollment.Target, Lifecycle: catalog.LifecycleActive, Fields: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Old name")}, Revision: "target-r1", ObservedAt: now}
	plan, err := catalog.BuildPlan(catalog.PlanRequest{Mapping: *target.Mapping, Source: source, Target: dest, Policies: c.Rules})
	if err != nil {
		t.Fatal(err)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	cmd := catalog.Command{ID: "command-" + suffix, Scope: c.Scope, MappingID: target.Enrollment.MappingID, MappingVersion: target.Enrollment.MappingVersion, PolicyRevision: strconv.FormatInt(c.Revision, 10), Source: source.Identity, Target: dest.Identity, Action: catalog.ActionUpdate, Values: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("New name")}, ExpectedSourceRevision: source.Revision, ExpectedTargetRevision: dest.Revision}
	decision := catalogsync.Decision{EnrollmentID: target.Enrollment.ID, Mapping: target.Mapping, Source: source, Target: dest, Plan: plan, Commands: []catalog.Command{cmd}, Ready: true}
	p := catalogsync.Preview{ID: "preview-" + suffix, Scope: c.Scope, ConfigRevision: c.Revision, Credentials: review.Credentials, Decisions: []catalogsync.Decision{decision}, Actor: "catalog-test-actor", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	p, err = db.SaveCatalogPreview(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func catalogSyncApprove(t testing.TB, db *postgres.Store, p catalogsync.Preview) catalogsync.Operation {
	t.Helper()
	op, err := db.ApproveCatalogPreview(context.Background(), catalogsync.Approval{Scope: p.Scope, PreviewID: p.ID, Actor: p.Actor, IdempotencyKey: "approval-" + p.ID}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func catalogSyncClaim(t testing.TB, db *postgres.Store) catalogsync.Work {
	t.Helper()
	now := time.Now().UTC()
	w, err := db.ClaimCatalogWork(context.Background(), catalogsync.ClaimRequest{WorkerID: "synthetic-catalog-worker", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil || w == nil {
		t.Fatalf("claim catalog work: %v", err)
	}
	return *w
}
