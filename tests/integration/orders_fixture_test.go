//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/postgres"
)

type ordersStoreFixture struct {
	db      *postgres.Store
	ctx     context.Context
	session identity.Session
	config  orders.Config
}

func newOrdersStoreFixture(t testing.TB) ordersStoreFixture {
	t.Helper()
	db := catalogSyncDB(t)
	ctx := context.Background()
	scope := orders.Scope{Tenant: "orders-tenant", OzonAccount: "orders-ozon", MoyskladAccount: "orders-moy"}
	session := ordersStoreOwner(t, db, scope.Tenant)
	ctx = identity.WithSession(ctx, session)
	ordersStoreConnections(t, db, scope)
	ordersStoreMappings(t, db, scope)
	config := orders.Config{Scope: scope, Enabled: true, TestEnabled: true, IntervalSeconds: 30, SourceFreshnessSeconds: 3600, PreviewTTLSeconds: 300, OrganizationID: "native-organization", AgentID: "native-agent", StoreID: "native-store", StateID: "native-state", SalesChannelID: "native-channel", FieldMappings: []orders.FieldMapping{{Source: "posting_number", Target: "name"}, {Source: "lines.quantity", Target: "positions.quantity"}, {Source: "lines.price", Target: "positions.price"}, {Source: "lines.vat", Target: "positions.vat"}}}
	config, err := db.SaveOrderConfig(ctx, orders.SaveConfigRequest{Config: config, Actor: session.AuditID, IdempotencyKey: "config-initial"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return ordersStoreFixture{db: db, ctx: ctx, session: session, config: config}
}

func ordersStoreOwner(t testing.TB, db *postgres.Store, tenant string) identity.Session {
	t.Helper()
	ctx := context.Background()
	owner := "u_" + strings.Repeat("1", 32)
	audit := "user." + strings.Repeat("a", 22)
	for _, statement := range []struct {
		query string
		args  []any
	}{{`INSERT INTO saas_users(id,email,password_hash,email_verified_at) VALUES($1,'orders-owner@example.test',$2,clock_timestamp())`, []any{owner, make([]byte, 40)}}, {`INSERT INTO saas_workspaces(id,name) VALUES($1,'Orders fixture')`, []any{tenant}}, {`INSERT INTO saas_memberships(workspace_id,user_id,role) VALUES($1,$2,'OWNER')`, []any{tenant, owner}}, {`INSERT INTO saas_merchant_accounts(user_id,tenant_id) VALUES($1,$2)`, []any{owner, tenant}}, {`INSERT INTO saas_sessions(audit_id,token_hash,csrf_token,user_id,user_auth_version,expires_at) VALUES($1,$2,$3,$4,1,clock_timestamp()+interval '7 hours')`, []any{audit, make([]byte, 32), strings.Repeat("c", 43), owner}}} {
		if _, err := db.Pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return identity.Session{AuditID: audit, User: identity.User{ID: owner, AuthVersion: 1}}
}

func ordersStoreConnections(t testing.TB, db *postgres.Store, scope orders.Scope) {
	t.Helper()
	for _, connection := range []struct{ provider, account string }{{"ozon", scope.OzonAccount}, {"moysklad", scope.MoyskladAccount}} {
		if err := db.RegisterStockConnection(context.Background(), scope.Tenant, connection.provider, connection.account, "orders synthetic fixture", true); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(context.Background(), `INSERT INTO connection_credentials(tenant_id,provider,account_id,name,revision,ciphertext,checked_at,last_request_id) VALUES($1,$2,$3,'Synthetic',1,$4,clock_timestamp(),'orders-synthetic')`, scope.Tenant, connection.provider, connection.account, make([]byte, 29)); err != nil {
			t.Fatal(err)
		}
	}
}

func ordersStoreMappings(t testing.TB, db *postgres.Store, scope orders.Scope) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	rfbs := false
	source := domain.InventoryImport{Account: scope.MoyskladAccount, StartedAt: now, FinishedAt: now, Entities: []domain.ExternalEntity{{Kind: "product", ID: "native-product", Name: "Synthetic item", Article: "orders-offer"}, {Kind: "store", ID: "native-store", Name: "Synthetic store"}}}
	market := domain.OzonImport{Account: scope.OzonAccount, StartedAt: now, FinishedAt: now, Products: []domain.OzonProduct{{ProductID: "1001", OfferID: "orders-offer", SKU: "2001"}}, Warehouses: []domain.OzonWarehouse{{ID: "ozon-warehouse", Name: "Synthetic warehouse", IsRFBS: &rfbs}}}
	if _, err := db.SaveInventoryImport(ctx, source, "orders-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveOzonImport(ctx, market, "orders-market"); err != nil {
		t.Fatal(err)
	}
	match := postgres.MatchScope{Tenant: scope.Tenant, OzonAccount: scope.OzonAccount, MoyskladAccount: scope.MoyskladAccount}
	if _, err := db.ConfirmMatch(ctx, match, "1001", "product", "native-product", "orders-fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ConfirmWarehouseMapping(ctx, match, "native-store", "ozon-warehouse", "orders-fixture"); err != nil {
		t.Fatal(err)
	}
}

func ordersStorePosting(scope orders.Scope, number, revision string, at time.Time) orders.Posting {
	return orders.Posting{Scope: scope, CommercialOrderID: "commercial-1", PostingNumber: number, OrderNumber: orders.Present("commercial-number-1"), Status: orders.Present("awaiting_deliver"), WarehouseID: orders.Present("ozon-warehouse"), DeliveryMethodID: orders.Present("delivery-method"), Lines: []orders.PostingLine{{Identity: orders.LineIdentity{SKU: "2001", OfferID: "orders-offer"}, ProductID: orders.Present("1001"), Quantity: "2", Price: orders.Present(orders.Money{Amount: "12.34", Currency: "RUB"}), VAT: orders.Present(orders.Decimal("20"))}}, Evidence: orders.EvidenceRevision{Revision: revision, Origin: orders.OriginOzon, ObservedAt: at, ProviderUpdatedAt: orders.Present(at)}}
}

func ordersStoreCommit(t testing.TB, fixture ordersStoreFixture, posting orders.Posting, key string) orders.Checkpoint {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	previous, err := fixture.db.OrderCheckpoint(fixture.ctx, fixture.config.Scope, "detail")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.db.ClaimOrderIngestion(fixture.ctx, orders.IngestionClaimRequest{Scope: fixture.config.Scope, Stream: "detail", RefreshKey: key, WorkerID: "orders-fixture", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil || lease == nil {
		t.Fatalf("claim ingestion: %v", err)
	}
	posting.Evidence.CollectionID = "fixture-collection"
	posting.Evidence.LocalSequence = lease.Sequence
	posting.Evidence.CollectionKind = orders.CollectionSerialDetail
	page := orders.SourcePage{Scope: fixture.config.Scope, ConfigRevision: lease.ConfigRevision, Credentials: lease.Credentials, Stream: "detail", RefreshKey: key, ExpectedCheckpointRevision: previous.Revision, Complete: true, WindowFrom: now.Add(-time.Hour), WindowTo: now, Postings: []orders.Posting{posting}, StartedAt: now, FinishedAt: now, IdempotencyKey: key, FenceToken: lease.Token, FreshDetail: true}
	checkpoint, err := fixture.db.CommitOrderPage(fixture.ctx, page)
	if err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

func ordersStorePreview(t testing.TB, fixture ordersStoreFixture, numbers ...string) orders.Preview {
	t.Helper()
	current, err := fixture.db.OrderReviewContext(fixture.ctx, fixture.config.Scope)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	id := fmt.Sprintf("preview-%d", time.Now().UnixNano())
	preview := orders.Preview{ID: id, Scope: fixture.config.Scope, ConfigRevision: current.Config.Revision, Credentials: current.Credentials, Actor: fixture.session.AuditID, CreatedAt: now, ExpiresAt: now.Add(time.Minute), Decisions: make([]orders.Decision, 0, len(numbers))}
	for _, number := range numbers {
		preview.Decisions = append(preview.Decisions, ordersStoreDecision(t, current, number, id, now))
	}
	preview, err = fixture.db.SaveOrderPreview(fixture.ctx, preview)
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func ordersStoreDecision(t testing.TB, current orders.ReviewContext, number, id string, now time.Time) orders.Decision {
	t.Helper()
	var posting orders.Posting
	var correlation orders.Correlation
	for _, candidate := range current.Postings {
		if candidate.PostingNumber == number {
			posting = candidate
		}
	}
	for _, candidate := range current.Correlations {
		if candidate.PostingNumber == number {
			correlation = candidate
		}
	}
	if posting.PostingNumber == "" || len(current.ProductMappings) != 1 || len(current.WarehouseMappings) != 1 {
		t.Fatal("complete scoped order fixture required")
	}
	config := current.Config
	write := orders.CustomerOrderWrite{OrganizationID: config.OrganizationID, AgentID: config.AgentID, StoreID: config.StoreID, StateID: config.StateID, SalesChannelID: config.SalesChannelID, Name: orders.Present(number), Positions: []orders.PositionWrite{{Identity: posting.Lines[0].Identity, Mapping: current.ProductMappings[0], Quantity: posting.Lines[0].Quantity, Price: posting.Lines[0].Price, VAT: posting.Lines[0].VAT}}}
	command := orders.Command{ID: id + ":" + number, Scope: config.Scope, Action: orders.ActionMirrorCreate, CommercialOrderID: posting.CommercialOrderID, PostingNumber: number, Correlation: correlation, ConfigRevision: config.Revision, Credentials: current.Credentials, ExpectedSourceRevision: posting.Evidence.Revision, SourceOrigin: posting.Evidence.Origin, Warehouse: current.WarehouseMappings[0], CustomerOrder: &write, IdempotencyKey: id + ":" + number, CreatedAt: now}
	return orders.Decision{Posting: posting, Correlation: correlation, Commands: []orders.Command{command}, Ready: true}
}

func ordersStoreApprove(t testing.TB, fixture ordersStoreFixture, preview orders.Preview) orders.Operation {
	t.Helper()
	operation, err := fixture.db.ApproveOrderPreview(fixture.ctx, orders.Approval{Scope: fixture.config.Scope, PreviewID: preview.ID, Actor: fixture.session.AuditID, IdempotencyKey: "approve-" + preview.ID}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func ordersStoreClaim(t testing.TB, fixture ordersStoreFixture, duration time.Duration) orders.Work {
	t.Helper()
	now := time.Now()
	work, err := fixture.db.ClaimOrderWork(fixture.ctx, orders.ClaimRequest{WorkerID: "orders-worker", Now: now, LeaseUntil: now.Add(duration)})
	if err != nil || work == nil {
		t.Fatalf("claim order work: %v", err)
	}
	return *work
}
