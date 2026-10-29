//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/postgres"
	"github.com/lilter96/styk/internal/testpublication"
	"golang.org/x/crypto/bcrypt"
)

func TestIdentitySessionsAndMembership(t *testing.T) {
	db, _ := controlledFixture(t)
	store, err := identity.New(db.Pool, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, workspace, err := store.Register(ctx, "owner@example.test", "long-password-owner", "Магазин A")
	if err != nil {
		t.Fatal(err)
	}
	b, other, err := store.Register(ctx, "other@example.test", "long-password-other", "Магазин B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Authenticate(ctx, a.Email, "long-password-owner"); !errors.Is(err, identity.ErrAuth) {
		t.Fatalf("unverified email authenticated: %v", err)
	}
	verifyTestEmail(t, store, a.Email)
	verifyTestEmail(t, store, b.Email)
	assertPasswordAndScopes(t, store, db, a, b, workspace, other)
	user, err := store.Authenticate(ctx, a.Email, "long-password-owner")
	if err != nil {
		t.Fatal(err)
	}
	session, token, err := store.CreateSession(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Session(ctx, token); err != nil {
		t.Fatal(err)
	}
	if err = store.SetMembership(ctx, session, workspace.ID, b.Email, "VIEWER"); err != nil {
		t.Fatal(err)
	}
	if role, err := store.Role(ctx, b.ID, workspace.ID); err != nil || role != "VIEWER" {
		t.Fatalf("viewer membership: %s %v", role, err)
	}
	if err = store.SetMembership(ctx, session, workspace.ID, b.Email, "OPERATOR"); err != nil {
		t.Fatal(err)
	}
	if role, err := store.Role(ctx, b.ID, workspace.ID); err != nil || role != "OPERATOR" {
		t.Fatalf("role change: %s %v", role, err)
	}
	if err = store.SetMembership(ctx, session, workspace.ID, a.Email, "VIEWER"); !errors.Is(err, identity.ErrLastOwner) {
		t.Fatalf("removed last owner: %v", err)
	}
	if err = store.SetMembership(ctx, session, workspace.ID, b.Email, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Role(ctx, b.ID, workspace.ID); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("revoked membership remained active: %v", err)
	}
	if err = store.RevokeSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Session(ctx, token); !errors.Is(err, identity.ErrAuth) {
		t.Fatalf("revoked session remained active: %v", err)
	}
}

func TestLegacyWorkspaceRequiresExplicitSingleClaim(t *testing.T) {
	db, _ := controlledFixture(t)
	store, err := identity.New(db.Pool, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scope := testpublication.Scope()
	if _, err = db.Pool.Exec(ctx, `INSERT INTO saas_workspaces(id,name,legacy_unclaimed) VALUES($1,'Existing test workspace',true)`, scope.Tenant); err != nil {
		t.Fatal(err)
	}
	a, _, err := store.Register(ctx, "claim-a@example.test", "long-password-owner", "A")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := store.Register(ctx, "claim-b@example.test", "long-password-owner", "B")
	if err != nil {
		t.Fatal(err)
	}
	verifyTestEmail(t, store, a.Email)
	verifyTestEmail(t, store, b.Email)
	if _, err = store.Role(ctx, a.ID, scope.Tenant); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("legacy workspace was claimed implicitly: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, user := range []identity.User{a, b} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			results <- store.ClaimLegacy(ctx, scope.Tenant, id)
		}(user.ID)
	}
	wg.Wait()
	close(results)
	success, denied := 0, 0
	for result := range results {
		if result == nil {
			success++
		} else if errors.Is(result, identity.ErrForbidden) {
			denied++
		} else {
			t.Fatal(result)
		}
	}
	var connections, owners, audits int
	err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM stock_connections WHERE tenant_id=$1),
	 (SELECT count(*) FROM saas_memberships WHERE workspace_id=$1 AND role='OWNER' AND revoked_at IS NULL),
	 (SELECT count(*) FROM saas_security_events WHERE workspace_id=$1 AND action='LOCAL_BOOTSTRAP_CLAIM')`, scope.Tenant).Scan(&connections, &owners, &audits)
	if err != nil || success != 1 || denied != 1 || connections != 2 || owners != 1 || audits != 1 {
		t.Fatalf("claim results success=%d denied=%d connections=%d owners=%d audits=%d err=%v", success, denied, connections, owners, audits, err)
	}
}

func TestPasswordResetInvalidatesSessionsAndOldAuthentications(t *testing.T) {
	db, _ := controlledFixture(t)
	store, err := identity.New(db.Pool, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	user, _, err := store.Register(ctx, "reset@example.test", "long-password-owner", "Магазин")
	if err != nil {
		t.Fatal(err)
	}
	verifyTestEmail(t, store, user.Email)
	stale, err := store.Authenticate(ctx, user.Email, "long-password-owner")
	if err != nil {
		t.Fatal(err)
	}
	_, sessionToken, err := store.CreateSession(ctx, stale)
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := store.CreateReset(ctx, user.Email)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := store.CreateReset(ctx, user.Email)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, token := range []string{first, second} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			results <- store.ResetPassword(ctx, value, "replacement-password")
		}(token)
	}
	wg.Wait()
	close(results)
	success, denied := 0, 0
	for result := range results {
		if result == nil {
			success++
		} else if errors.Is(result, identity.ErrAuth) {
			denied++
		} else {
			t.Fatal(result)
		}
	}
	if success != 1 || denied != 1 {
		t.Fatalf("concurrent reset outcomes: success=%d denied=%d", success, denied)
	}
	if _, err = store.Session(ctx, sessionToken); !errors.Is(err, identity.ErrAuth) {
		t.Fatalf("reset did not invalidate session: %v", err)
	}
	if _, _, err = store.CreateSession(ctx, stale); !errors.Is(err, identity.ErrAuth) {
		t.Fatalf("pre-reset authentication created session: %v", err)
	}
	if _, err = store.Authenticate(ctx, user.Email, "replacement-password"); err != nil {
		t.Fatal(err)
	}
}

func verifyTestEmail(t *testing.T, store *identity.Store, email string) {
	t.Helper()
	ctx := context.Background()
	address, token, err := store.CreateVerification(ctx, email)
	if err != nil || address != email || token == "" {
		t.Fatalf("verification intent: %v", err)
	}
	if err = store.VerifyEmail(ctx, token); err != nil {
		t.Fatal(err)
	}
	if err = store.VerifyEmail(ctx, token); !errors.Is(err, identity.ErrAuth) {
		t.Fatalf("verification token reused: %v", err)
	}
}

func assertPasswordAndScopes(t *testing.T, store *identity.Store, db *postgres.Store, a, b identity.User, first, second identity.Workspace) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.Authenticate(ctx, a.Email, "wrong-password"); !errors.Is(err, identity.ErrAuth) {
		t.Fatalf("wrong password accepted: %v", err)
	}
	if _, err := store.Role(ctx, a.ID, second.ID); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("cross-workspace role: %v", err)
	}
	if _, err := store.Role(ctx, b.ID, first.ID); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("reverse cross-workspace role: %v", err)
	}
	var hash []byte
	if err := db.Pool.QueryRow(ctx, `SELECT password_hash FROM saas_users WHERE id=$1`, a.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost(hash); err != nil || cost < 12 || bcrypt.CompareHashAndPassword(hash, []byte("long-password-owner")) != nil {
		t.Fatal("password was not stored using the configured bcrypt cost")
	}
}
