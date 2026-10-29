//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrdersStoreMetadataRegistryAndOneShotCreation(t *testing.T) {
	fixture := newOrdersStoreFixture(t)
	registry := orders.MetadataRegistry{Scope: fixture.config.Scope, ObservedAt: time.Now().UTC(), Attributes: []orders.AttributeDefinition{{UniqueCode: "styk-order-status", AttributeID: "11111111-1111-4111-8111-111111111111", Type: "string"}}}
	registry, err := fixture.db.SaveOrderMetadata(fixture.ctx, registry, 0)
	if err != nil || registry.Revision != 1 {
		t.Fatalf("save native attribute UUID: %+v %v", registry, err)
	}
	conflicting := registry
	conflicting.Attributes = []orders.AttributeDefinition{{UniqueCode: "styk-order-status", AttributeID: "22222222-2222-4222-8222-222222222222", Type: "string"}}
	if _, err = fixture.db.SaveOrderMetadata(fixture.ctx, conflicting, 1); !errors.Is(err, orders.ErrConflict) {
		t.Fatalf("uniqueCode rebound to another UUID: %v", err)
	}
	request := orders.MetadataCreationRequest{Scope: registry.Scope, UniqueCode: "styk-posting-number", Type: "string", Actor: fixture.session.AuditID, IdempotencyKey: "create-posting-attribute"}
	intent, err := fixture.db.BeginOrderMetadataCreation(fixture.ctx, request, time.Now())
	if err != nil || !intent.DispatchPermitted {
		t.Fatalf("new metadata intent not journaled: %+v %v", intent, err)
	}
	replay, err := fixture.db.BeginOrderMetadataCreation(fixture.ctx, request, time.Now())
	if err != nil || replay.ID != intent.ID || replay.DispatchPermitted {
		t.Fatalf("metadata creation replay could send another POST: %+v %v", replay, err)
	}
	completion := orders.MetadataCreationCompletion{IntentID: intent.ID, Scope: intent.Scope, ExpectedState: orders.StateDispatching, State: orders.StateUnknown, ObservedAt: time.Now().UTC()}
	if _, err = fixture.db.CompleteOrderMetadataCreation(fixture.ctx, completion); err != nil {
		t.Fatal(err)
	}
	completion.ExpectedState, completion.State = orders.StateUnknown, orders.StateReconciled
	completion.AttributeID = "33333333-3333-4333-8333-333333333333"
	registry, err = fixture.db.CompleteOrderMetadataCreation(fixture.ctx, completion)
	if err != nil || registry.Revision != 2 || len(registry.Attributes) != 2 {
		t.Fatalf("metadata readback did not register native UUID: %+v %v", registry, err)
	}
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_metadata_creation_intents`, 1)
	assertOrdersRowCount(t, fixture, `SELECT count(*) FROM orders_metadata_creation_results`, 2)
}
