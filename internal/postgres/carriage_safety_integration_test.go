//go:build integration

package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/identity"
	"github.com/lilter96/styk/internal/orders"
)

func TestCarriagePostgresGenericWorkersCannotConsumeIntent(t *testing.T) {
	f := carriagePG(t)
	tests := []struct {
		name  string
		claim func() (*domain.Task, error)
	}{
		{"all", func() (*domain.Task, error) { return f.db.Claim(f.ctx, time.Minute) }},
		{"account", func() (*domain.Task, error) {
			return f.db.ClaimForWorker(f.ctx, time.Minute, f.config.Scope.OzonAccount)
		}},
		{"catalog", func() (*domain.Task, error) {
			return f.db.ClaimForCatalogWorker(f.ctx, time.Minute, f.config.Scope.OzonAccount, f.config.Scope.MoyskladAccount)
		}},
		{"stored credentials", func() (*domain.Task, error) {
			return f.db.ClaimForCredentialWorker(f.ctx, time.Minute, f.config.Scope.OzonAccount, f.config.Scope.MoyskladAccount)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task, err := tc.claim()
			if err != nil || task != nil {
				t.Fatal("generic worker consumed carriage intent", task, err)
			}
		})
	}
	var attempts int
	var state string
	err := f.db.Pool.QueryRow(f.ctx, `SELECT attempts,state FROM outbox_tasks WHERE carriage_work_id=$1`, f.work.ID).Scan(&attempts, &state)
	if err != nil || attempts != 0 || state != "pending" {
		t.Fatal("generic claim altered carriage lease", attempts, state, err)
	}
	work := f.claim(t)
	if work.ID != f.work.ID || work.State != orders.StateClaimed || work.DispatchCount != 0 {
		t.Fatal("specialized worker lost carriage intent", work)
	}
}

type carriagePGFixture struct {
	orderInventoryFixture
	actor   string
	preview orders.CarriagePreview
	work    orders.CarriageWork
}

func carriagePG(t *testing.T) carriagePGFixture {
	t.Helper()
	f := orderInventoryDBFixture(t)
	session, ok := identity.SessionFromContext(f.ctx)
	if !ok {
		t.Fatal("owner absent")
	}
	enabled := true
	var err error
	f.config, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: session.AuditID, IdempotencyKey: "carriage-read-consent", ReadEnabled: &enabled}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	preview := carriagePGPreview(t, f.config, session.AuditID, "carriage-preview")
	preview, err = f.db.SaveCarriagePreview(f.ctx, preview)
	if err != nil {
		t.Fatal(err)
	}
	approval := orders.CarriageApproval{Scope: f.config.Scope, PreviewID: preview.ID, Actor: session.AuditID, IdempotencyKey: "carriage-approval", AllReadyConsent: true, PhysicalReadinessConfirmed: true}
	work, err := f.db.ApproveCarriagePreview(f.ctx, approval, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.db.ApproveCarriagePreview(f.ctx, approval, time.Now())
	if err != nil || replay.ID != work.ID {
		t.Fatal("approval replay changed work", err)
	}
	return carriagePGFixture{orderInventoryFixture: f, actor: session.AuditID, preview: preview, work: work}
}

func carriagePGPreview(t *testing.T, config orders.Config, actor, id string) orders.CarriagePreview {
	t.Helper()
	now := time.Now().UTC()
	traceable := false
	member := orders.CarriageMember{PostingNumber: "carriage-posting", Status: "awaiting_deliver", Ready: true, SourceFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Lines: []orders.PostingLine{{Identity: orders.LineIdentity{SKU: "2001", OfferID: "offer"}, Quantity: "1"}}, Blockers: []orders.Validation{}}
	member.Scheme, member.WarehouseID, member.DeliveryMethodID = "FBS", "9002", "9001"
	member.Capability = &orders.CapabilityRef{ID: "carriage-profile", Revision: 1, WarehouseID: "9002", DeliveryMethodID: "9001", Scheme: "FBS", Express: orders.Present(false), VerifiedDimensions: []string{"scheme", "warehouse", "express"}, ObservedAt: now, ValidUntil: now.Add(5 * time.Minute)}
	snapshot := orders.CarriageSnapshot{Scope: config.Scope, DeliveryMethodID: "9001", WarehouseID: "9002", Members: []orders.CarriageMember{member}, CandidateIDs: []string{}, ContractVersion: "synthetic-storage-contract", Complete: true, ObservedAt: now, Blockers: []orders.Validation{}}
	var err error
	snapshot.Fingerprint, err = orders.CarriageSnapshotFingerprint(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	plan := orders.CarriagePlan{ID: id, Scope: config.Scope, CarriageInput: orders.CarriageInput{Action: orders.CarriageCreate, DeliveryMethodID: "9001", AllBLRTraceable: &traceable}, WarehouseID: "9002", Members: snapshot.Members, Snapshot: snapshot, ConfigRevision: config.Revision, Credentials: orders.CredentialRevisions{Ozon: 1, Moysklad: 1}, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	plan.MembershipFingerprint, err = orders.CarriageMembershipFingerprint(plan.Members)
	if err != nil {
		t.Fatal(err)
	}
	plan.Fingerprint, err = orders.CarriagePlanFingerprint(plan)
	if err != nil || plan.Validate() != nil {
		t.Fatal("synthetic storage plan invalid", err, plan.Validate())
	}
	return orders.CarriagePreview{ID: id, Plan: plan, Actor: actor, Validation: []orders.Validation{}}
}

func (f carriagePGFixture) claim(t *testing.T) orders.CarriageWork {
	t.Helper()
	now := time.Now()
	work, err := f.db.ClaimCarriageWork(f.ctx, orders.ClaimRequest{WorkerID: "carriage-pg-worker", Now: now, LeaseUntil: now.Add(time.Minute)})
	if err != nil || work == nil {
		t.Fatal("claim missing", err)
	}
	return *work
}

func TestCarriagePostgresUnknownCannotResendAndPayloadIsImmutable(t *testing.T) {
	f := carriagePG(t)
	work := f.claim(t)
	allowed, err := f.db.GuardCarriageDispatch(f.ctx, work, true)
	if err != nil || !allowed {
		t.Fatal("admission missing", allowed, err)
	}
	receipt := orders.Receipt{CommandID: work.Plan.ID, State: orders.StateUnknown, ObservedAt: time.Now().UTC(), Code: "TRANSPORT_UNCERTAIN"}
	err = f.db.CompleteCarriageWork(f.ctx, orders.CarriageCompletion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: orders.StateDispatching, State: orders.StateUnknown, Receipt: receipt})
	if err != nil {
		t.Fatal(err)
	}
	read := f.claim(t)
	allowed, err = f.db.GuardCarriageDispatch(f.ctx, read, true)
	if err != nil || allowed || read.DispatchCount != 1 {
		t.Fatal("unknown resend admitted", allowed, err)
	}
	allowed, err = f.db.GuardCarriageDispatch(f.ctx, read, false)
	if err != nil || !allowed {
		t.Fatal("unknown readback denied", allowed, err)
	}
	queries := []string{`DELETE FROM orders_carriage_work WHERE id=$1`, `UPDATE orders_carriage_work SET work=jsonb_set(work,'{plan,delivery_method_id}','"9999"') WHERE id=$1`, `UPDATE orders_carriage_work SET state='QUEUED',work=jsonb_set(work,'{state}','"QUEUED"') WHERE id=$1`, `DELETE FROM outbox_tasks WHERE carriage_work_id=$1`, `UPDATE outbox_tasks SET carriage_ozon_account='foreign' WHERE carriage_work_id=$1`}
	for _, query := range queries {
		_, err = f.db.Pool.Exec(f.ctx, query, work.ID)
		if err == nil {
			t.Fatal("immutable carriage evidence mutated")
		}
	}
}

func TestCarriagePostgresRevocationAndNoDispatchReconciliationFailClosed(t *testing.T) {
	f := carriagePG(t)
	work := f.claim(t)
	err := f.db.CompleteCarriageWork(f.ctx, orders.CarriageCompletion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: orders.StateClaimed, State: orders.StateReconciled})
	if !errors.Is(err, orders.ErrStale) {
		t.Fatal("zero dispatch reconciled", err)
	}
	enabled := false
	_, err = f.db.SaveOrderConfig(f.ctx, orders.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: f.actor, IdempotencyKey: "carriage-revoke", ReadEnabled: &enabled}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := f.db.GuardCarriageDispatch(f.ctx, work, true)
	if err != nil || allowed {
		t.Fatal("revoked authority admitted", allowed, err)
	}
	var count int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT dispatch_count FROM orders_carriage_work WHERE id=$1`, work.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("denied admission dispatched", count, err)
	}
}

func TestCarriagePostgresOpenHeadAndForeignScopeCannotCreateCompetingBatch(t *testing.T) {
	f := carriagePG(t)
	preview := carriagePGPreview(t, f.config, f.actor, "carriage-preview-second")
	var err error
	preview, err = f.db.SaveCarriagePreview(f.ctx, preview)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.db.ApproveCarriagePreview(f.ctx, orders.CarriageApproval{Scope: f.config.Scope, PreviewID: preview.ID, Actor: f.actor, IdempotencyKey: "competing-batch", AllReadyConsent: true, PhysicalReadinessConfirmed: true}, time.Now())
	if !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("competing global head accepted", err)
	}
	foreign := f.config.Scope
	foreign.Tenant = "foreign"
	if _, err = readCarriageWork(f.ctx, f.db.Pool, foreign, f.work.ID); !errors.Is(err, orders.ErrNotFound) {
		t.Fatal("foreign work leaked", err)
	}
	var count int
	if err = f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM outbox_tasks WHERE task_type='orders_carriage'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("approval rollback left competing task", count, err)
	}
}
