//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/postgres"
)

func TestCatalogSyncConfigRevisionAndTenantIsolation(t *testing.T) {
	db, c := catalogSyncFixture(t)
	ctx := context.Background()
	req := catalogsync.SaveConfigRequest{Config: c, ExpectedRevision: 1, Actor: "catalog-test-actor", IdempotencyKey: "config-update"}
	c2, err := db.SaveCatalogConfig(ctx, req, time.Now())
	if err != nil || c2.Revision != 2 {
		t.Fatalf("revision: %d %v", c2.Revision, err)
	}
	duplicate, err := db.SaveCatalogConfig(ctx, req, time.Now())
	if err != nil || duplicate.Revision != c2.Revision {
		t.Fatalf("idempotent config: %v", err)
	}
	req.Config.IntervalSeconds = 120
	if _, err = db.SaveCatalogConfig(ctx, req, time.Now()); !errors.Is(err, catalogsync.ErrConflict) {
		t.Fatalf("different config reused key: %v", err)
	}
	req.IdempotencyKey = "wrong-version"
	if _, err = db.SaveCatalogConfig(ctx, req, time.Now()); !errors.Is(err, catalogsync.ErrStale) {
		t.Fatalf("stale revision: %v", err)
	}
	foreign := c.Scope
	foreign.Tenant = "unrelated-merchant"
	if _, err = db.CatalogConfig(ctx, foreign); !errors.Is(err, catalogsync.ErrNotFound) {
		t.Fatalf("foreign config: %v", err)
	}
	if _, err = db.CatalogReviewContext(ctx, foreign); !errors.Is(err, catalogsync.ErrNotFound) {
		t.Fatalf("foreign context: %v", err)
	}
	var revisions int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM catalog_sync_config_revisions`).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatalf("revision audit: %d %v", revisions, err)
	}
}

func TestCatalogSyncApprovalAtomicityAndOperatorKey(t *testing.T) {
	db, c := catalogSyncFixture(t)
	p := catalogSyncPreview(t, db, c)
	ctx := context.Background()
	a := catalogsync.Approval{Scope: c.Scope, PreviewID: p.ID, Actor: p.Actor, IdempotencyKey: "same-operator-key"}
	catalogSyncConcurrentApproval(t, db, a)
	a.EnrollmentIDs = []string{"enrolled-1"}
	if _, err := db.ApproveCatalogPreview(ctx, a, time.Now()); !errors.Is(err, catalogsync.ErrConflict) {
		t.Fatalf("changed operator request: %v", err)
	}
	var approvals, tasks int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM catalog_sync_approvals),(SELECT count(*) FROM outbox_tasks WHERE task_type='catalog_sync')`).Scan(&approvals, &tasks); err != nil || approvals != 1 || tasks != 1 {
		t.Fatalf("atomic approval: approvals=%d tasks=%d %v", approvals, tasks, err)
	}
	if task, err := db.Claim(ctx, time.Minute); err != nil || task != nil {
		t.Fatalf("mock claimed catalog write: %v %v", task, err)
	}
}

func TestCatalogSyncApprovalRollsBackWholeBatch(t *testing.T) {
	db, c := catalogSyncFixture(t)
	second := c.Targets[0]
	second.ID = "enrolled-2"
	c.Targets = append(c.Targets, second)
	c, err := db.SaveCatalogConfig(context.Background(), catalogsync.SaveConfigRequest{Config: c, ExpectedRevision: 1, Actor: "catalog-test-actor", IdempotencyKey: "duplicate-targets"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p := catalogSyncPreview(t, db, c)
	duplicate := p.Decisions[0]
	duplicate.EnrollmentID = second.ID
	duplicate.Commands = append([]catalog.Command(nil), duplicate.Commands...)
	duplicate.Commands[0].ID += "-second"
	p.ID += "-two-targets"
	p.Decisions = append(p.Decisions, duplicate)
	if _, err = db.SaveCatalogPreview(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ApproveCatalogPreview(context.Background(), catalogsync.Approval{Scope: c.Scope, PreviewID: p.ID, Actor: p.Actor, IdempotencyKey: "atomic-batch"}, time.Now()); !errors.Is(err, catalogsync.ErrConflict) {
		t.Fatalf("expected active target conflict: %v", err)
	}
	var approvals, operations, work, tasks int
	if err = db.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM catalog_sync_approvals),(SELECT count(*) FROM catalog_sync_operations),(SELECT count(*) FROM catalog_sync_work),(SELECT count(*) FROM outbox_tasks WHERE task_type='catalog_sync')`).Scan(&approvals, &operations, &work, &tasks); err != nil || approvals+operations+work+tasks != 0 {
		t.Fatalf("partial approval persisted: %d/%d/%d/%d %v", approvals, operations, work, tasks, err)
	}
}

func TestCatalogSyncDispatchIsSingleAndExpiredOutcomeUnknown(t *testing.T) {
	db, c := catalogSyncFixture(t)
	p := catalogSyncPreview(t, db, c)
	catalogSyncApprove(t, db, p)
	w := catalogSyncClaim(t, db)
	ctx := context.Background()
	ok, err := db.GuardCatalogDispatch(ctx, catalogsync.DispatchGuard{Work: w, Now: time.Now(), Begin: true})
	if err != nil || !ok {
		t.Fatalf("begin dispatch: %v %v", ok, err)
	}
	ok, err = db.GuardCatalogDispatch(ctx, catalogsync.DispatchGuard{Work: w, Now: time.Now(), Begin: true})
	if err != nil || ok {
		t.Fatalf("second dispatch fence: %v %v", ok, err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE outbox_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE catalog_work_id=$1`, w.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	next, err := db.ClaimCatalogWork(ctx, catalogsync.ClaimRequest{WorkerID: "restarted-worker", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil || next != nil {
		t.Fatalf("expired dispatch replayed: %v %v", next, err)
	}
	page, err := db.CatalogHistory(ctx, c.Scope, catalogsync.PageRequest{Limit: 10})
	if err != nil || page.Operations[0].Targets[0].State != catalogsync.StateUnknown {
		t.Fatalf("unknown outcome: %+v %v", page, err)
	}
	var dispatches, commands int
	if err = db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM catalog_sync_attempts WHERE phase='DISPATCHING'),(SELECT count(*) FROM catalog_sync_commands)`).Scan(&dispatches, &commands); err != nil || dispatches != 1 || commands != 1 {
		t.Fatalf("dispatch journal: %d/%d %v", dispatches, commands, err)
	}
	if err = db.CompleteCatalogWork(ctx, catalogsync.Completion{WorkID: w.ID, Scope: w.Scope, ClaimToken: w.ClaimToken, ExpectedState: catalogsync.StateDispatching, State: catalogsync.StateRejected, Now: now}); err == nil {
		t.Fatal("stale worker completed expired dispatch")
	}
}

func TestCatalogSyncDispatchRevalidatesConfigCredentialsAndCommand(t *testing.T) {
	tests := []struct{ name string }{{"configuration"}, {"credentials"}, {"command"}}
	for _, tc := range tests {
		change := tc.name
		t.Run(tc.name, func(t *testing.T) {
			db, c := catalogSyncFixture(t)
			p := catalogSyncPreview(t, db, c)
			catalogSyncApprove(t, db, p)
			w := catalogSyncClaim(t, db)
			ctx := context.Background()
			switch change {
			case "configuration":
				c.IntervalSeconds = 120
				if _, err := db.SaveCatalogConfig(ctx, catalogsync.SaveConfigRequest{Config: c, ExpectedRevision: 1, Actor: "catalog-test-actor", IdempotencyKey: "changed-before-dispatch"}, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "credentials":
				if _, err := db.Pool.Exec(ctx, `UPDATE connection_credentials SET revision=revision+1 WHERE provider='ozon'`); err != nil {
					t.Fatal(err)
				}
			case "command":
				w.Command.Values[catalog.FieldName] = catalog.Text("substituted")
			}
			ok, err := db.GuardCatalogDispatch(ctx, catalogsync.DispatchGuard{Work: w, Now: time.Now(), Begin: true})
			if err != nil || ok {
				t.Fatalf("stale %s dispatched: %v %v", change, ok, err)
			}
		})
	}
}

func catalogSyncConcurrentApproval(t *testing.T, db *postgres.Store, a catalogsync.Approval) {
	t.Helper()
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan catalogsync.Operation, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			op, err := db.ApproveCatalogPreview(ctx, a, time.Now())
			results <- op
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	for op := range results {
		if id != "" && id != op.ID {
			t.Fatal("approval replay created another operation")
		}
		id = op.ID
	}
}

func TestCatalogSyncConcurrentClaimsFenceOneWork(t *testing.T) {
	db, c := catalogSyncFixture(t)
	p := catalogSyncPreview(t, db, c)
	catalogSyncApprove(t, db, p)
	now := time.Now()
	req := catalogsync.ClaimRequest{WorkerID: "concurrent-catalog-worker", Now: now, LeaseUntil: now.Add(time.Minute)}
	type result struct {
		work *catalogsync.Work
		err  error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, err := db.ClaimCatalogWork(context.Background(), req)
			results <- result{w, err}
		}()
	}
	wg.Wait()
	close(results)
	claimed := 0
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.work != nil {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("concurrent claims=%d", claimed)
	}
}

func TestCatalogSyncUnknownTargetDoesNotFenceAnotherEnrollment(t *testing.T) {
	db, c := catalogSyncFixture(t)
	c, second := catalogSyncSecondEnrollment(t, db, c)
	ctx := context.Background()
	var err error
	p := catalogSyncPreview(t, db, c)
	catalogSyncApprove(t, db, p)
	w := catalogSyncClaim(t, db)
	if ok, err := db.GuardCatalogDispatch(ctx, catalogsync.DispatchGuard{Work: w, Now: time.Now(), Begin: true}); err != nil || !ok {
		t.Fatalf("dispatch: %v %v", ok, err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE outbox_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE catalog_work_id=$1`, w.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if next, err := db.ClaimCatalogWork(ctx, catalogsync.ClaimRequest{WorkerID: "expire-unknown", Now: now, LeaseUntil: now.Add(time.Minute)}); err != nil || next != nil {
		t.Fatalf("unknown recovery replay: %v %v", next, err)
	}
	review, err := db.CatalogReviewContext(ctx, c.Scope)
	if err != nil || !review.Targets[0].Fenced || review.Targets[1].Fenced {
		t.Fatalf("target fence widened: %+v %v", review, err)
	}
	c.Targets = []catalogsync.Enrollment{second, c.Targets[0]}
	c, err = db.SaveCatalogConfig(ctx, catalogsync.SaveConfigRequest{Config: c, ExpectedRevision: 2, Actor: "catalog-test-actor", IdempotencyKey: "select-second"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p = catalogSyncPreview(t, db, c)
	catalogSyncApprove(t, db, p)
	other := catalogSyncClaim(t, db)
	if other.EnrollmentID != second.ID || other.Command.Target.ID != "1002" {
		t.Fatal("unrelated enrolled target was blocked by unknown outcome")
	}
}

func catalogSyncSecondEnrollment(t *testing.T, db *postgres.Store, c catalogsync.Config) (catalogsync.Config, catalogsync.Enrollment) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ConfirmMatch(ctx, postgres.MatchScope{Tenant: c.Scope.Tenant, MoyskladAccount: c.Scope.MoyskladAccount, OzonAccount: c.Scope.OzonAccount}, "1002", "product", "source-2", "catalog-isolation-test"); err != nil {
		t.Fatal(err)
	}
	second := c.Targets[0]
	second.ID = "enrolled-2"
	second.Source.ID = "source-2"
	second.Target.ID = "1002"
	second.Target.OfferID = "shared-2"
	if err := db.Pool.QueryRow(ctx, `SELECT id::text FROM product_mappings WHERE ozon_product_id='1002'`).Scan(&second.MappingID); err != nil {
		t.Fatal(err)
	}
	c.Targets = append(c.Targets, second)
	var err error
	c, err = db.SaveCatalogConfig(ctx, catalogsync.SaveConfigRequest{Config: c, ExpectedRevision: 1, Actor: "catalog-test-actor", IdempotencyKey: "enroll-second"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c, second
}
