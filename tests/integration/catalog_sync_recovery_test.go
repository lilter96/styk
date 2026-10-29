//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/postgres"
)

func TestCatalogSyncAsyncReceiptSurvivesRestartWithoutWriteReplay(t *testing.T) {
	db, c := catalogSyncFixture(t)
	p := catalogSyncPreview(t, db, c)
	catalogSyncApprove(t, db, p)
	w := catalogSyncClaim(t, db)
	ctx := context.Background()
	if ok, err := db.GuardCatalogDispatch(ctx, catalogsync.DispatchGuard{Work: w, Now: time.Now()}); err != nil || !ok {
		t.Fatalf("initial claimed guard: %v %v", ok, err)
	}
	if ok, err := db.GuardCatalogDispatch(ctx, catalogsync.DispatchGuard{Work: w, Now: time.Now(), Begin: true}); err != nil || !ok {
		t.Fatalf("dispatch: %v %v", ok, err)
	}
	receipt := catalog.Receipt{CommandID: w.Command.ID, State: catalog.ReceiptAccepted, HTTPStatus: 200, TaskID: "async-test-task"}
	if err := db.CompleteCatalogWork(ctx, catalogsync.Completion{Scope: w.Scope, WorkID: w.ID, ClaimToken: w.ClaimToken, ExpectedState: catalogsync.StateDispatching, State: catalogsync.StateProcessing, Receipt: receipt, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	restarted, readback := catalogSyncRecoverAsync(t, db, w, receipt)
	catalogSyncReconcile(t, restarted, readback, nil)
	context, err := restarted.CatalogReviewContext(ctx, c.Scope)
	if err != nil || context.Targets[0].Baseline == nil || context.Targets[0].Fenced {
		t.Fatalf("baseline/fence: %+v %v", context, err)
	}
	source, target := p.Decisions[0].Source, p.Decisions[0].Target
	target.Fields = map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("New name")}
	plan, err := catalog.BuildPlan(catalog.PlanRequest{Mapping: *context.Targets[0].Mapping, Source: source, Target: target, Policies: c.Rules, Baseline: context.Targets[0].Baseline})
	if err != nil || !plan.Noop {
		t.Fatalf("repeat synchronized data is not noop: %+v %v", plan, err)
	}
}

func catalogSyncReconcile(t testing.TB, db *postgres.Store, w catalogsync.Work, created *catalog.Identity) {
	t.Helper()
	source, target := w.Decision.Source, w.Decision.Target
	if created != nil {
		target.Identity = *created
	}
	target.Fields = make(map[catalog.Field]catalog.Value, len(w.Command.Values))
	for field, value := range w.Command.Values {
		target.Fields[field] = value
	}
	target.Revision = "verified-target"
	target.ObservedAt = time.Now().UTC()
	source.ObservedAt = target.ObservedAt
	baseline, err := catalog.NewBaseline(source, target)
	if err != nil {
		t.Fatal(err)
	}
	completion := catalogsync.Completion{Scope: w.Scope, WorkID: w.ID, ClaimToken: w.ClaimToken, ExpectedState: w.State, State: catalogsync.StateReconciled, Receipt: w.Receipt, CreatedTarget: created, Baseline: &baseline, ObservedSource: &source, ObservedTarget: &target, Now: time.Now()}
	if err = db.CompleteCatalogWork(context.Background(), completion); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogSyncCreatedAssociationPersistsWithoutPhysicalMapping(t *testing.T) {
	db, c := catalogSyncFixture(t)
	c, p, e := catalogSyncCreationPreview(t, db, c)
	var err error
	cmd := p.Decisions[0].Commands[0]
	catalogSyncApprove(t, db, p)
	w := catalogSyncClaim(t, db)
	if ok, err := db.GuardCatalogDispatch(context.Background(), catalogsync.DispatchGuard{Work: w, Now: time.Now(), Begin: true}); err != nil || !ok {
		t.Fatalf("begin create: %v %v", ok, err)
	}
	created := e.Target
	created.ID = "9001"
	receipt := catalog.Receipt{CommandID: cmd.ID, State: catalog.ReceiptApplied, HTTPStatus: 200, ExternalID: created.ID}
	if err = db.CompleteCatalogWork(context.Background(), catalogsync.Completion{Scope: w.Scope, WorkID: w.ID, ClaimToken: w.ClaimToken, ExpectedState: catalogsync.StateDispatching, State: catalogsync.StateAcknowledged, Receipt: receipt, CreatedTarget: &created, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	w = catalogSyncClaim(t, db)
	catalogSyncReconcile(t, db, w, &created)
	current, err := db.CatalogReviewContext(context.Background(), c.Scope)
	if err != nil || current.Targets[0].CreatedTarget == nil || current.Targets[0].CreatedTarget.ID != created.ID || current.Targets[0].Mapping != nil {
		t.Fatalf("created association lost or physical mapping fabricated: %+v %v", current, err)
	}
	var mappings int
	if err = db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM product_mappings`).Scan(&mappings); err != nil || mappings != 1 {
		t.Fatalf("physical mapping changed: %d %v", mappings, err)
	}
	p.ID = "attempt-duplicate-create"
	if _, err = db.SaveCatalogPreview(context.Background(), p); err == nil {
		t.Fatal("resolved creation could be previewed for another create")
	}
}

func catalogSyncRecoverAsync(t *testing.T, db *postgres.Store, w catalogsync.Work, receipt catalog.Receipt) (*postgres.Store, catalogsync.Work) {
	t.Helper()
	ctx := context.Background()
	restarted, err := postgres.Open(ctx, db.Pool.Config().ConnString(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Pool.Close)
	poll := catalogSyncClaim(t, restarted)
	if poll.State != catalogsync.StateProcessing || poll.Receipt.TaskID != receipt.TaskID || poll.ClaimToken == w.ClaimToken {
		t.Fatalf("durable asynchronous receipt lost: %+v", poll)
	}
	if ok, err := restarted.GuardCatalogDispatch(ctx, catalogsync.DispatchGuard{Work: poll, Now: time.Now(), Begin: true}); err != nil || ok {
		t.Fatalf("processing work could redispatch: %v %v", ok, err)
	}
	if _, err = restarted.Pool.Exec(ctx, `UPDATE outbox_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE catalog_work_id=$1`, poll.ID); err != nil {
		t.Fatal(err)
	}
	recovered := catalogSyncClaim(t, restarted)
	if recovered.State != catalogsync.StateProcessing || recovered.ClaimToken == poll.ClaimToken {
		t.Fatal("poll recovery changed phase or reused fence")
	}
	late := catalogsync.Completion{Scope: poll.Scope, WorkID: poll.ID, ClaimToken: poll.ClaimToken, ExpectedState: catalogsync.StateProcessing, State: catalogsync.StateAcknowledged, Receipt: receipt, Now: time.Now()}
	if err = restarted.CompleteCatalogWork(ctx, late); err == nil {
		t.Fatal("old poll token completed newer lease")
	}
	receipt.State = catalog.ReceiptApplied
	late.ClaimToken = recovered.ClaimToken
	late.Receipt = receipt
	if err = restarted.CompleteCatalogWork(ctx, late); err != nil {
		t.Fatal(err)
	}
	readback := catalogSyncClaim(t, restarted)
	return restarted, readback
}

func catalogSyncCreationPreview(t *testing.T, db *postgres.Store, c catalogsync.Config) (catalogsync.Config, catalogsync.Preview, catalogsync.Enrollment) {
	t.Helper()
	return catalogSyncSaveCreationPreview(t, db, c, catalogSyncCreationEnrollment(c))
}

func catalogSyncCreationEnrollment(c catalogsync.Config) catalogsync.Enrollment {
	e := c.Targets[0]
	e.ID = "create-explicit"
	e.MappingID = ""
	e.MappingVersion = 0
	e.Source.ID = "source-2"
	e.Target.ID = ""
	e.Target.OfferID = "new-created-offer"
	e.Create = &catalogsync.Creation{ID: "stable-create-id", ExternalCode: "explicit-marker", Values: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Created name")}}
	return e
}

func catalogSyncSaveCreationPreview(t *testing.T, db *postgres.Store, c catalogsync.Config, e catalogsync.Enrollment) (catalogsync.Config, catalogsync.Preview, catalogsync.Enrollment) {
	t.Helper()
	c.Targets = []catalogsync.Enrollment{e}
	var err error
	c, err = db.SaveCatalogConfig(context.Background(), catalogsync.SaveConfigRequest{Config: c, ExpectedRevision: 1, Actor: "catalog-test-actor", IdempotencyKey: "enroll-create"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source := catalog.Product{Identity: e.Source, Revision: "create-source-r1", ObservedAt: now, Lifecycle: catalog.LifecycleActive, Fields: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Created name")}}
	cmd := catalog.Command{ID: "create-exact-command", Scope: c.Scope, PolicyRevision: "2", Source: e.Source, Target: e.Target, Action: catalog.ActionCreate, Values: e.Create.Values, CreateID: e.Create.ID, CreateExternalCode: e.Create.ExternalCode, IdempotencyKey: "durable-create", ExpectedSourceRevision: source.Revision}
	d := catalogsync.Decision{EnrollmentID: e.ID, Source: source, Target: catalog.Product{Identity: e.Target}, TargetAbsent: true, Commands: []catalog.Command{cmd}, Ready: true}
	p := catalogsync.Preview{ID: "create-preview", Scope: c.Scope, ConfigRevision: c.Revision, Credentials: catalogsync.CredentialRevisions{Moysklad: 1, Ozon: 1}, Decisions: []catalogsync.Decision{d}, Actor: "catalog-test-actor", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if _, err = db.SaveCatalogPreview(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return c, p, e
}

func TestCatalogSyncRejectsReceiptIdentitySubstitution(t *testing.T) {
	tests := []struct {
		name    string
		receipt catalog.Receipt
	}{{"external product", catalog.Receipt{ExternalID: "unrelated"}}, {"result product", catalog.Receipt{Products: []catalog.ProductReceipt{{ID: "unrelated"}}}}, {"result offer", catalog.Receipt{Products: []catalog.ProductReceipt{{ID: "1001", OfferID: "unrelated"}}}}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, c := catalogSyncFixture(t)
			p := catalogSyncPreview(t, db, c)
			catalogSyncApprove(t, db, p)
			w := catalogSyncClaim(t, db)
			if ok, err := db.GuardCatalogDispatch(context.Background(), catalogsync.DispatchGuard{Work: w, Now: time.Now(), Begin: true}); err != nil || !ok {
				t.Fatalf("dispatch: %v %v", ok, err)
			}
			r := tc.receipt
			r.CommandID = w.Command.ID
			r.State = catalog.ReceiptApplied
			r.HTTPStatus = 200
			err := db.CompleteCatalogWork(context.Background(), catalogsync.Completion{Scope: w.Scope, WorkID: w.ID, ClaimToken: w.ClaimToken, ExpectedState: catalogsync.StateDispatching, State: catalogsync.StateAcknowledged, Receipt: r, Now: time.Now()})
			if err == nil {
				t.Fatal("foreign receipt identity advanced operation")
			}
			var receipts int
			if err = db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM catalog_sync_receipts`).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("substituted receipt persisted: %d %v", receipts, err)
			}
		})
	}
}
