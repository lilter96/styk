//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func orderReadAuthorityFixture(t *testing.T, grant bool) orderInventoryFixture {
	t.Helper()
	f := orderInventoryDBFixture(t)
	config := f.config
	config.Enabled, config.TestEnabled = true, true
	if grant {
		config.Automation = &orders.AutomationGrant{Actions: []orders.Action{orders.ActionMirrorCreate}, Fields: []string{"name"}, ExpiresAt: time.Now().Add(time.Hour)}
	}
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: "user.aaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "read-authority-enable"}, time.Now())
	if err != nil {
		t.Fatal("save explicit read permission", err)
	}
	return f
}

func orderReadAuthorityCheck(t *testing.T, f orderInventoryFixture, config orders.Config) error {
	t.Helper()
	tx, err := f.db.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	return validateOrderReadAuthority(context.Background(), tx, config)
}

func TestOrderReadAuthorityOutlivesBrowserSessionButNotMerchantAuthority(t *testing.T) {
	cases := []struct {
		name, mutation string
		blocked        bool
	}{
		{"ordinary background read", "", false},
		{"logout", `UPDATE saas_sessions SET revoked_at=clock_timestamp()`, false},
		{"session expiry", `UPDATE saas_sessions SET expires_at=created_at+interval '1 microsecond'`, false},
		{"owner disabled", `UPDATE saas_users SET disabled_at=clock_timestamp()`, true},
		{"owner membership revoked", `UPDATE saas_memberships SET revoked_at=clock_timestamp()`, true},
		{"email verification revoked", `UPDATE saas_users SET email_verified_at=NULL`, true},
		{"legacy owner unclaimed", `UPDATE saas_workspaces SET legacy_unclaimed=true`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := orderReadAuthorityFixture(t, false)
			if tc.mutation != "" {
				inventoryExec(t, f.db, tc.mutation)
			}
			err := orderReadAuthorityCheck(t, f, f.config)
			if tc.blocked && !errors.Is(err, orders.ErrBlocked) || !tc.blocked && err != nil {
				t.Fatalf("background read authority mismatch: %v", err)
			}
		})
	}
}

func TestOrderReadAuthorityRejectsDisabledAndSubstitutedConfig(t *testing.T) {
	f := orderReadAuthorityFixture(t, false)
	stale := f.config
	config := f.config
	config.Enabled = false
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: "user.aaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "read-authority-disable"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []orders.Config{f.config, stale} {
		if !errors.Is(orderReadAuthorityCheck(t, f, candidate), orders.ErrBlocked) {
			t.Fatal("disabled read permission or stale enabled revision survived")
		}
	}
	stale.Scope.MoyskladAccount = "other-account"
	if !errors.Is(orderReadAuthorityCheck(t, f, stale), orders.ErrBlocked) {
		t.Fatal("provider account substitution acquired read permission")
	}
}

func TestOrderReadAuthorityChecksRecordedAutomationRevocation(t *testing.T) {
	f := orderReadAuthorityFixture(t, true)
	if err := orderReadAuthorityCheck(t, f, f.config); err != nil {
		t.Fatal("valid recorded automation rejected", err)
	}
	config := f.config
	grant := *config.Automation
	now := time.Now()
	grant.RevokedAt, config.Automation = &now, &grant
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: "user.aaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "read-authority-revoke-grant"}, now)
	if err != nil {
		t.Fatal("save immutable revocation revision", err)
	}
	forged := f.config
	grant = *forged.Automation
	grant.RevokedAt, forged.Automation = nil, &grant
	if !errors.Is(orderReadAuthorityCheck(t, f, forged), orders.ErrBlocked) {
		t.Fatal("cached grant omitted recorded revocation")
	}
}

func TestOrderReadAuthorityChecksDBGrantExpiryDespiteUnexpiredCachedGrant(t *testing.T) {
	f := orderReadAuthorityFixture(t, true)
	config := f.config
	grant := *config.Automation
	grant.ExpiresAt, config.Automation = time.Now().Add(150*time.Millisecond), &grant
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: config, ExpectedRevision: config.Revision, Actor: "user.aaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "read-authority-short-grant"}, time.Now())
	if err != nil {
		t.Fatal("save short-lived immutable grant", err)
	}
	forged := f.config
	grant = *forged.Automation
	grant.ExpiresAt, forged.Automation = time.Now().Add(time.Hour), &grant
	time.Sleep(160 * time.Millisecond)
	if !errors.Is(orderReadAuthorityCheck(t, f, forged), orders.ErrBlocked) {
		t.Fatal("DB-expired grant survived a future cached expiry")
	}
}
