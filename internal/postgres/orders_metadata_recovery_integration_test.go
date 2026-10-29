//go:build integration

package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
)

func TestMetadataReadRecoveryRequiresCurrentOwnerAndNeverGrantsDispatch(t *testing.T) {
	fixture := orderInventoryDBFixture(t)
	intent, actor := metadataGuardDBIntent(t, fixture)
	completion := orders.MetadataCreationCompletion{IntentID: intent.ID, Scope: intent.Scope, ExpectedState: orders.StateDispatching, State: orders.StateUnknown, ObservedAt: time.Now().UTC()}
	if _, err := fixture.db.CompleteOrderMetadataCreation(fixture.ctx, completion); err != nil {
		t.Fatal(err)
	}
	old, ok := identity.SessionFromContext(fixture.ctx)
	if !ok {
		t.Fatal("owner session missing")
	}
	current := old
	current.AuditID = "user." + strings.Repeat("b", 22)
	inventoryExec(t, fixture.db, `INSERT INTO saas_sessions(audit_id,token_hash,csrf_token,user_id,user_auth_version,expires_at) VALUES($1,$2,$3,$4,1,clock_timestamp()+interval '1 hour')`, current.AuditID, []byte(strings.Repeat("x", 32)), strings.Repeat("d", 43), current.User.ID)
	inventoryExec(t, fixture.db, `UPDATE saas_sessions SET revoked_at=clock_timestamp() WHERE audit_id=$1`, actor)
	ctx := identity.WithSession(context.Background(), current)
	observed, err := fixture.db.ReadOrderMetadataCreation(ctx, intent.Scope, intent.UniqueCode, current.AuditID)
	if err != nil || observed.ID != intent.ID || observed.DispatchPermitted || observed.State != orders.StateUnknown {
		t.Fatalf("new current session read: %+v %v", observed, err)
	}
	if _, err = fixture.db.ReadOrderMetadataCreation(fixture.ctx, intent.Scope, intent.UniqueCode, actor); !errors.Is(err, orders.ErrBlocked) {
		t.Fatalf("revoked original session: %v", err)
	}
	foreign := intent.Scope
	foreign.OzonAccount = "foreign-account"
	if _, err = fixture.db.ReadOrderMetadataCreation(ctx, foreign, intent.UniqueCode, current.AuditID); !errors.Is(err, orders.ErrNotFound) {
		t.Fatalf("another account pair: %v", err)
	}
	var attempts int
	if err = fixture.db.Pool.QueryRow(ctx, `SELECT count(*) FROM orders_metadata_creation_results WHERE intent_id=$1 AND result->>'phase'='DISPATCH_AUTHORIZED'`, intent.ID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("read recovery granted HTTP dispatch: count=%d err=%v", attempts, err)
	}
}
