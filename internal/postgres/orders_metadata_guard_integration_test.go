//go:build integration

package postgres

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
)

func metadataGuardDBIntent(t *testing.T, fixture orderInventoryFixture) (orders.MetadataCreationIntent, string) {
	t.Helper()
	session, ok := identity.SessionFromContext(fixture.ctx)
	if !ok {
		t.Fatal("metadata guard owner session missing")
	}
	request := orders.MetadataCreationRequest{Scope: fixture.config.Scope, UniqueCode: "styk-order-status", Type: "string", Actor: session.AuditID, IdempotencyKey: "metadata-guard"}
	intent, err := fixture.db.BeginOrderMetadataCreation(fixture.ctx, request, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return intent, session.AuditID
}

func TestMetadataGuardPostgresConcurrentAdmissionConsumesOneDispatch(t *testing.T) {
	fixture := orderInventoryDBFixture(t)
	intent, actor := metadataGuardDBIntent(t, fixture)
	results := make(chan bool, 2)
	errorsSeen := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			allowed, err := fixture.db.GuardOrderMetadataCreation(fixture.ctx, intent, actor, 1)
			results <- allowed
			errorsSeen <- err
		})
	}
	workers.Wait()
	close(results)
	close(errorsSeen)
	admitted, stale := 0, 0
	for allowed := range results {
		if allowed {
			admitted++
		}
	}
	for err := range errorsSeen {
		if errors.Is(err, orders.ErrStale) {
			stale++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if admitted != 1 || stale != 1 {
		t.Fatalf("admitted=%d stale=%d", admitted, stale)
	}
	var attempts int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM orders_metadata_creation_results WHERE intent_id=$1 AND result->>'phase'='DISPATCH_AUTHORIZED'`, intent.ID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("durable dispatch attempts=%d err=%v", attempts, err)
	}
}

func TestMetadataGuardPostgresCredentialRevisionAndOwnerAreCurrent(t *testing.T) {
	fixture := orderInventoryDBFixture(t)
	intent, actor := metadataGuardDBIntent(t, fixture)
	if allowed, err := fixture.db.GuardOrderMetadataCreation(fixture.ctx, intent, actor, 2); allowed || !errors.Is(err, orders.ErrStale) {
		t.Fatalf("stale credential allowed=%v err=%v", allowed, err)
	}
	inventoryExec(t, fixture.db, `UPDATE saas_memberships SET revoked_at=clock_timestamp() WHERE workspace_id=$1`, intent.Scope.Tenant)
	if allowed, err := fixture.db.GuardOrderMetadataCreation(fixture.ctx, intent, actor, 1); allowed || !errors.Is(err, orders.ErrBlocked) {
		t.Fatalf("revoked owner allowed=%v err=%v", allowed, err)
	}
	var attempts int
	if err := fixture.db.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM orders_metadata_creation_results WHERE intent_id=$1`, intent.ID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("unauthorized dispatch journal=%d err=%v", attempts, err)
	}
}

func TestMetadataGuardPostgresRetainsUnknownHistoryAndDiagnosticOnReadback(t *testing.T) {
	fixture := orderInventoryDBFixture(t)
	intent, actor := metadataGuardDBIntent(t, fixture)
	if allowed, err := fixture.db.GuardOrderMetadataCreation(fixture.ctx, intent, actor, 1); err != nil || !allowed {
		t.Fatalf("guard allowed=%v err=%v", allowed, err)
	}
	completion := orders.MetadataCreationCompletion{IntentID: intent.ID, Scope: intent.Scope, ExpectedState: orders.StateDispatching, State: orders.StateUnknown, ObservedAt: time.Now().UTC(), Code: "POST_RESPONSE_UNVERIFIED"}
	if registry, err := fixture.db.CompleteOrderMetadataCreation(fixture.ctx, completion); err != nil || len(registry.Attributes) != 0 {
		t.Fatalf("unknown prematurely registered UUID: %+v %v", registry, err)
	}
	completion.ExpectedState, completion.State, completion.AttributeID = orders.StateUnknown, orders.StateReconciled, "11111111-1111-4111-8111-111111111111"
	completion.Code = "CONFIRMED_BY_MARKER_AFTER_RESPONSE_DRIFT"
	registry, err := fixture.db.CompleteOrderMetadataCreation(fixture.ctx, completion)
	if err != nil || len(registry.Attributes) != 1 {
		t.Fatalf("readback registry=%+v err=%v", registry, err)
	}
	var unknown, confirmed int
	if err = fixture.db.Pool.QueryRow(fixture.ctx, `SELECT count(*) FILTER(WHERE result->>'code'='POST_RESPONSE_UNVERIFIED'),count(*) FILTER(WHERE result->>'code'='CONFIRMED_BY_MARKER_AFTER_RESPONSE_DRIFT') FROM orders_metadata_creation_results WHERE intent_id=$1`, intent.ID).Scan(&unknown, &confirmed); err != nil || unknown != 1 || confirmed != 1 {
		t.Fatalf("immutable outcome history unknown=%d confirmed=%d err=%v", unknown, confirmed, err)
	}
}
