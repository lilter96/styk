//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/orders"
)

func savePermanentOrders(t *testing.T, f *ordersStoreFixture, consent bool, key string) (orders.Config, error) {
	t.Helper()
	config := f.config
	config.Automation = &orders.AutomationGrant{Perpetual: true, Actions: []orders.Action{orders.ActionMirrorCreate, orders.ActionMirrorUpdate}, Fields: []string{"name", "state", "positions.quantity", "positions.price", "positions.vat"}}
	return f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: key, AutomationConsent: consent}, time.Now())
}

func TestOrdersPermanentOptInRoundTripAndNarrowing(t *testing.T) {
	f := newOrdersStoreFixture(t)
	if _, err := savePermanentOrders(t, &f, false, "without-consent"); !errors.Is(err, orders.ErrBlocked) {
		t.Fatalf("permanent opt-in without consent: %v", err)
	}
	config, err := savePermanentOrders(t, &f, true, "permanent")
	if err != nil {
		t.Fatal(err)
	}
	f.config = config
	loaded, err := f.db.OrderConfig(f.ctx, config.Scope)
	if err != nil || !loaded.Automation.Active(time.Now().Add(48*time.Hour), config.Scope, config.Revision) {
		t.Fatalf("permanent authority did not survive reload: %v", err)
	}
	var expiryNull bool
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT expires_at IS NULL FROM orders_automation_grants WHERE id=$1 AND revision=$2`, config.Automation.ID, config.Automation.Revision).Scan(&expiryNull); err != nil || !expiryNull {
		t.Fatal("permanent grant stored a fake expiry", err)
	}
	config.IntervalSeconds = 120
	saved, err := f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "unrelated"}, time.Now())
	if err != nil || !saved.Automation.Perpetual || saved.StoreID != f.config.StoreID {
		t.Fatalf("unrelated save lost policy or legacy data: %v", err)
	}
	config = saved
	config.Automation.Fields = append(config.Automation.Fields, "moment")
	if _, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "expansion"}, time.Now()); !errors.Is(err, orders.ErrBlocked) {
		t.Fatalf("silent policy expansion: %v", err)
	}
}

func TestOrdersPermanentRevocationStopsClaimedWork(t *testing.T) {
	f := newOrdersStoreFixture(t)
	config, err := savePermanentOrders(t, &f, true, "permanent")
	if err != nil {
		t.Fatal(err)
	}
	f.config = config
	posting := ordersStorePosting(config.Scope, "permanent-queued", "source", time.Now().UTC())
	ordersStoreCommit(t, f, posting, "permanent-source")
	preview := ordersStorePreview(t, f, posting.PostingNumber)
	preview.ID += "-automatic"
	preview.Automatic = true
	preview.Actor = fmt.Sprintf("AUTOMATION:%s:%d", config.Automation.ID, config.Automation.Revision)
	preview.Decisions[0].Commands[0].AutomationGrantRevision = config.Automation.Revision
	if _, err = f.db.SaveOrderPreview(context.Background(), preview); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ApproveOrderPreview(context.Background(), orders.Approval{Scope: config.Scope, PreviewID: preview.ID, Actor: preview.Actor, Automatic: true, IdempotencyKey: "permanent-queue"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	work := ordersStoreClaim(t, f, time.Minute)
	config.Automation = nil
	if _, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "disable-permanent"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if allowed, _ := f.db.GuardOrderDispatch(context.Background(), orders.DispatchGuard{Work: work, Begin: true}); allowed {
		t.Fatal("revoked queued automatic work retained write authority")
	}
	assertOrdersRowCount(t, f, `SELECT count(*) FROM orders_write_intents`, 0)
}

func TestOrdersLegacyGrantRequiresPermanentOptIn(t *testing.T) {
	f := newOrdersStoreFixture(t)
	config := f.config
	config.Automation = &orders.AutomationGrant{ExpiresAt: time.Now().Add(time.Hour), Actions: []orders.Action{orders.ActionMirrorCreate}, Fields: []string{"name"}}
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: f.session.AuditID, IdempotencyKey: "legacy"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = savePermanentOrders(t, &f, false, "silent-upgrade"); !errors.Is(err, orders.ErrBlocked) {
		t.Fatalf("legacy grant silently became permanent: %v", err)
	}
	if _, err = savePermanentOrders(t, &f, true, "opt-in"); err != nil {
		t.Fatal(err)
	}
}

func TestOrdersPermanentMigrationRefusesToRewriteImmutableHistory(t *testing.T) {
	f := newOrdersStoreFixture(t)
	config, err := savePermanentOrders(t, &f, true, "permanent-migration")
	if err != nil {
		t.Fatal(err)
	}
	before := permanentMigrationAudit(t, f)
	assertPermanentMigration69DownRefuses(t, f)
	if after := permanentMigrationAudit(t, f); after != before {
		t.Fatal("refused exact migration69 downgrade changed schema version or immutable authorization source")
	}
	if err = f.db.RequireCurrentSchema(f.ctx); err != nil {
		t.Fatal("failed downgrade changed current schema", err)
	}
	loaded, err := f.db.OrderConfig(f.ctx, config.Scope)
	if err != nil || loaded.Automation == nil || !loaded.Automation.Active(time.Now().Add(48*time.Hour), config.Scope, config.Revision) {
		t.Fatal("failed downgrade altered authorization history", err)
	}
}

// Exercise the authorization migration itself; the latest schema may contain
// unrelated later migrations, whose Down contract is a separate test.
func assertPermanentMigration69DownRefuses(t *testing.T, f ordersStoreFixture) {
	t.Helper()
	raw, err := os.ReadFile("../../migrations/00069_orders_perpetual_automation.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, found := strings.Cut(string(raw), "-- +goose Down")
	if !found {
		t.Fatal("exact migration69 Down section missing")
	}
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	tx, err := f.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLeaseTest(t, tx)
	_, refused := tx.Exec(ctx, down)
	var database *pgconn.PgError
	if !errors.As(refused, &database) || database.Code != "P0001" || !strings.Contains(database.Message, "Permanent order authorization history exists") {
		t.Fatal("exact migration69 downgrade did not preserve permanent authorization audit")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal("rollback exact migration69 guard probe", err)
	}
}

func permanentMigrationAudit(t *testing.T, f ordersStoreFixture) string {
	t.Helper()
	var snapshot string
	err := f.db.Pool.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'grants',(SELECT COALESCE(jsonb_agg(to_jsonb(g) ORDER BY g.id,g.revision),'[]'::jsonb) FROM orders_automation_grants g),
 'revisions',(SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.tenant_id,r.moysklad_account,r.ozon_account,r.revision),'[]'::jsonb) FROM orders_config_revisions r),
 'schema_versions',(SELECT COALESCE(jsonb_agg(to_jsonb(v) ORDER BY v.id),'[]'::jsonb) FROM goose_db_version v))::text`).Scan(&snapshot)
	if err != nil {
		t.Fatal("read immutable authorization and schema audit", err)
	}
	return snapshot
}
