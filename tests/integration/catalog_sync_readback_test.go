//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/postgres"
)

func TestCatalogSyncPersistsOnlyVerifiedSelectedOzonAttributes(t *testing.T) {
	tests := []struct {
		name, actual string
		matches      bool
	}{
		{"provider defaults", `[{"id":1,"values":[{"value":"selected"}]},{"id":4191,"values":[{"value":"provider description"}]}]`, true},
		{"missing selected", `[{"id":4191,"values":[{"value":"provider description"}]}]`, false},
		{"changed selected", `[{"id":1,"values":[{"value":"changed"}]}]`, false},
		{"duplicate selected", `[{"id":1,"values":[{"value":"selected"}]},{"id":1,"values":[{"value":"selected"}]}]`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, w, created := catalogSyncAttributeWork(t)
			completion := catalogSyncAttributeCompletion(t, w, created, tc.actual)
			err := db.CompleteCatalogWork(context.Background(), completion)
			if (tc.matches && err != nil) || (!tc.matches && !errors.Is(err, catalogsync.ErrStale)) {
				t.Fatalf("selected attributes accepted=%v, error=%v", tc.matches, err)
			}
			var baselines int
			if err = db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM catalog_sync_baselines`).Scan(&baselines); err != nil {
				t.Fatal(err)
			}
			if (baselines == 1) != tc.matches {
				t.Fatalf("invalid reconciliation evidence persisted: count=%d", baselines)
			}
		})
	}
}

func catalogSyncAttributeWork(t *testing.T) (*postgres.Store, catalogsync.Work, catalog.Identity) {
	t.Helper()
	db, c := catalogSyncFixture(t)
	e := catalogSyncCreationEnrollment(c)
	e.Create.Values[catalog.FieldAttributes] = catalog.Value{State: catalog.StateKnown, Data: json.RawMessage(`[{"id":1,"values":[{"value":"selected"}]}]`)}
	_, p, e := catalogSyncSaveCreationPreview(t, db, c, e)
	catalogSyncApprove(t, db, p)
	w := catalogSyncClaim(t, db)
	ctx := context.Background()
	if ok, err := db.GuardCatalogDispatch(ctx, catalogsync.DispatchGuard{Work: w, Now: time.Now(), Begin: true}); err != nil || !ok {
		t.Fatalf("begin selected attributes dispatch: %v %v", ok, err)
	}
	created := e.Target
	created.ID = "9001"
	receipt := catalog.Receipt{CommandID: w.Command.ID, State: catalog.ReceiptApplied, HTTPStatus: 200, ExternalID: created.ID}
	completion := catalogsync.Completion{Scope: w.Scope, WorkID: w.ID, ClaimToken: w.ClaimToken, ExpectedState: catalogsync.StateDispatching, State: catalogsync.StateAcknowledged, Receipt: receipt, CreatedTarget: &created, Now: time.Now()}
	if err := db.CompleteCatalogWork(ctx, completion); err != nil {
		t.Fatal(err)
	}
	return db, catalogSyncClaim(t, db), created
}

func catalogSyncAttributeCompletion(t *testing.T, w catalogsync.Work, created catalog.Identity, attributes string) catalogsync.Completion {
	t.Helper()
	source, target := w.Decision.Source, w.Decision.Target
	target.Identity = created
	target.Fields = map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Created name"), catalog.FieldAttributes: {State: catalog.StateKnown, Data: json.RawMessage(attributes)}}
	target.Revision = "verified-selected-attributes"
	target.ObservedAt = time.Now().UTC()
	baseline, err := catalog.NewBaseline(source, target)
	if err != nil {
		t.Fatal(err)
	}
	return catalogsync.Completion{Scope: w.Scope, WorkID: w.ID, ClaimToken: w.ClaimToken, ExpectedState: w.State, State: catalogsync.StateReconciled, Receipt: w.Receipt, CreatedTarget: &created, Baseline: &baseline, ObservedSource: &source, ObservedTarget: &target, Now: time.Now()}
}
