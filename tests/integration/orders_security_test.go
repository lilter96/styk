//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersStoreAutomationUsesStableOwnerAndRevocation(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	config := fixture.config
	config.Automation = &orders.AutomationGrant{OwnerID: "untrusted-browser-owner", ExpiresAt: time.Now().Add(time.Hour), Actions: []orders.Action{orders.ActionMirrorCreate}, Fields: []string{"name", "state", "positions.quantity", "positions.price", "positions.vat"}}
	var err error
	fixture.config, err = fixture.db.SaveOrderConfig(fixture.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: fixture.session.AuditID, IdempotencyKey: "automation-grant"}, time.Now())
	if err != nil || fixture.config.Automation.OwnerID != fixture.session.User.ID {
		t.Fatalf("grant owner was not resolved from authenticated owner: %v", err)
	}
	posting := ordersStorePosting(config.Scope, "granted-posting", "source-1", time.Now().UTC().Truncate(time.Microsecond))
	ordersStoreCommit(t, fixture, posting, "grant-ingestion")
	preview := ordersStorePreview(t, fixture, posting.PostingNumber)
	preview.ID += "-automatic"
	preview.Automatic = true
	preview.Actor = fmt.Sprintf("AUTOMATION:%s:%d", fixture.config.Automation.ID, fixture.config.Automation.Revision)
	for i := range preview.Decisions {
		preview.Decisions[i].Commands[0].ID += "-automatic"
		preview.Decisions[i].Commands[0].AutomationGrantRevision = fixture.config.Automation.Revision
	}
	if _, err = fixture.db.SaveOrderPreview(context.Background(), preview); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.db.ApproveOrderPreview(context.Background(), orders.Approval{Scope: config.Scope, PreviewID: preview.ID, Actor: preview.Actor, IdempotencyKey: "automatic-approval", Automatic: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.db.Pool.Exec(fixture.ctx, `UPDATE saas_sessions SET revoked_at=clock_timestamp() WHERE audit_id=$1`, fixture.session.AuditID); err != nil {
		t.Fatal(err)
	}
	work := ordersStoreClaim(t, fixture, time.Minute)
	if allowed, guardErr := fixture.db.GuardOrderDispatch(context.Background(), orders.DispatchGuard{Work: work}); guardErr != nil || !allowed {
		t.Fatalf("background grant depended on expired browser cookie: %v %v", allowed, guardErr)
	}
	if _, err = fixture.db.Pool.Exec(fixture.ctx, `UPDATE saas_memberships SET revoked_at=clock_timestamp() WHERE workspace_id=$1 AND user_id=$2`, config.Scope.Tenant, fixture.session.User.ID); err != nil {
		t.Fatal(err)
	}
	if allowed, guardErr := fixture.db.GuardOrderDispatch(context.Background(), orders.DispatchGuard{Work: work, Begin: true}); allowed || !errors.Is(guardErr, orders.ErrBlocked) {
		t.Fatalf("revoked owner retained background write authority: %v %v", allowed, guardErr)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_write_intents`, 0)
}

func TestOrdersStoreCiphertextIsolationAndExpiry(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	config := fixture.config
	config.PIIEnabled, config.PIIRetentionSeconds = true, 3600
	var err error
	fixture.config, err = fixture.db.SaveOrderConfig(fixture.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: fixture.session.AuditID, IdempotencyKey: "pii-policy"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	posting := ordersStorePosting(config.Scope, "secret-posting", "source-1", now)
	ordersStoreCommit(t, fixture, posting, "secret-ingest")
	secret := orders.PostingSecret{Scope: config.Scope, PostingNumber: posting.PostingNumber, Revision: 1, Purpose: "order-customer", Ciphertext: bytes.Repeat([]byte{7}, 29), CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute)}
	if err = fixture.db.SaveOrderSecret(fixture.ctx, secret); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.db.OrderSecret(fixture.ctx, secret.Scope, secret.PostingNumber, secret.Revision)
	if err != nil || !bytes.Equal(stored.Ciphertext, secret.Ciphertext) {
		t.Fatalf("encrypted secret did not round-trip: %v", err)
	}
	otherScope := secret.Scope
	otherScope.Tenant += "-other"
	if _, err = fixture.db.OrderSecret(fixture.ctx, otherScope, secret.PostingNumber, secret.Revision); !errors.Is(err, orders.ErrNotFound) {
		t.Fatalf("cross-tenant ciphertext leaked: %v", err)
	}
	secret.Ciphertext = bytes.Repeat([]byte{8}, 29)
	if err = fixture.db.SaveOrderSecret(fixture.ctx, secret); !errors.Is(err, orders.ErrConflict) {
		t.Fatalf("immutable encrypted revision changed: %v", err)
	}
	_, err = fixture.db.Pool.Exec(fixture.ctx, `INSERT INTO orders_posting_secrets(tenant_id,moysklad_account,ozon_account,posting_number,revision,purpose,ciphertext,created_at,expires_at) VALUES($1,$2,$3,$4,2,'order-customer',$5,clock_timestamp()-interval '1 hour',clock_timestamp()-interval '1 minute')`, secret.Scope.Tenant, secret.Scope.MoyskladAccount, secret.Scope.OzonAccount, secret.PostingNumber, secret.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.db.OrderSecret(fixture.ctx, secret.Scope, secret.PostingNumber, 2); !errors.Is(err, orders.ErrNotFound) {
		t.Fatalf("expired ciphertext remained readable: %v", err)
	}
	if count, deleteErr := fixture.db.DeleteExpiredOrderSecrets(fixture.ctx, time.Now(), 10); deleteErr != nil || count != 1 {
		t.Fatalf("expired PII retention deletion: count=%d error=%v", count, deleteErr)
	}
}

func TestOrdersStoreProjectionAndReviewTenantIsolation(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	posting := ordersStorePosting(fixture.config.Scope, "isolated-posting", "source-1", time.Now().UTC().Truncate(time.Microsecond))
	ordersStoreCommit(t, fixture, posting, "isolated-ingest")
	preview := ordersStorePreview(t, fixture, posting.PostingNumber)
	ordersStoreApprove(t, fixture, preview)
	other := fixture.config.Scope
	other.Tenant += "-other"
	if _, err := fixture.db.OrderPreview(fixture.ctx, other, preview.ID); !errors.Is(err, orders.ErrNotFound) {
		t.Fatalf("review leaked across tenant: %v", err)
	}
	page, err := fixture.db.OrderPostings(fixture.ctx, other, orders.PageRequest{Limit: 10})
	if err != nil || len(page.Postings) != 0 {
		t.Fatalf("posting leaked across tenant: %+v %v", page, err)
	}
	_, err = fixture.db.Pool.Exec(fixture.ctx, `INSERT INTO orders_posting_heads(tenant_id,moysklad_account,ozon_account,posting_number,observation_id,active,checked_at,updated_at) SELECT tenant_id||'-other',moysklad_account,ozon_account,posting_number,id,true,clock_timestamp(),clock_timestamp() FROM orders_posting_observations LIMIT 1`)
	if err == nil {
		t.Fatal("composite observation FK accepted cross-tenant head")
	}
	_, err = fixture.db.Pool.Exec(fixture.ctx, `INSERT INTO orders_projection_bindings(tenant_id,moysklad_account,ozon_account,posting_number,sync_id,marker,created_at) VALUES($1,$2,$3,$4,'forged-sync','forged-marker',clock_timestamp())`, other.Tenant, other.MoyskladAccount, other.OzonAccount, posting.PostingNumber)
	if err == nil {
		t.Fatal("projection binding accepted cross-tenant posting identity")
	}
}
