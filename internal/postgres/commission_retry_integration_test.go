//go:build integration

package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func commissionRetryBlocked(t *testing.T, f *commissionPGFixture, code string) orders.CommissionWork {
	t.Helper()
	work := f.claim(t)
	err := f.db.CompleteCommissionWork(f.ctx, orders.CommissionCompletion{Scope: work.Scope, WorkID: work.ID, ClaimToken: work.ClaimToken, ExpectedState: orders.StateClaimed, State: orders.StateBlocked, Code: code})
	if err != nil {
		t.Fatal(err)
	}
	work.State, work.Code = orders.StateBlocked, code
	return work
}

func commissionRetryPreview(t *testing.T, f commissionPGFixture, id string) orders.CommissionPreview {
	t.Helper()
	plan := f.preview.Plan
	plan.ID, plan.CreatedAt = id, time.Now().UTC()
	plan.ExpiresAt = plan.CreatedAt.Add(15 * time.Minute)
	var err error
	plan.ExternalCode, err = orders.CommissionExternalCode(plan.Scope, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan.Fingerprint, err = orders.CommissionPlanFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ExternalCode == f.preview.Plan.ExternalCode {
		t.Fatal("retry must exercise a different native marker")
	}
	preview, err := f.db.SaveCommissionPreview(f.ctx, orders.CommissionPreview{ID: id, Plan: plan, Actor: f.actor})
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func commissionRetryAssertHead(t *testing.T, f commissionPGFixture, id string, count int) {
	t.Helper()
	var head string
	var works, tasks int
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT work_id FROM orders_finance_periods`).Scan(&head); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM orders_finance_work`).Scan(&works); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Pool.QueryRow(f.ctx, `SELECT count(*) FROM outbox_tasks WHERE task_type='orders_finance'`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if head != id || works != count || tasks != count {
		t.Fatalf("head=%s works=%d tasks=%d want head=%s count=%d", head, works, tasks, id, count)
	}
}

func TestCommissionPostgresRetryCannotBypassNativeDocumentIdentity(t *testing.T) {
	for _, code := range []string{"FINANCE_EXISTING_DOCUMENT_REQUIRES_REVIEW", "FINANCE_EXISTING_DOCUMENT_UNVERIFIED"} {
		t.Run(code, func(t *testing.T) {
			f := commissionPG(t)
			old := commissionRetryBlocked(t, &f, code)
			preview := commissionRetryPreview(t, f, "identity-conflict-retry")
			_, err := f.db.ApproveCommissionPreview(f.ctx, orders.CommissionApproval{Scope: old.Scope, PreviewID: preview.ID, Actor: f.actor, IdempotencyKey: "identity-conflict-retry", RetryWorkID: old.ID}, time.Now())
			if !errors.Is(err, orders.ErrBlocked) {
				t.Fatal("native identity fence bypassed by new marker", err)
			}
			commissionRetryAssertHead(t, f, old.ID, 1)
		})
	}
}

func TestCommissionPostgresRetryPermitsOrdinaryKnownUnsentBlock(t *testing.T) {
	for _, code := range []string{"FINANCE_AUTHORITY_CHANGED", "FINANCE_SOURCE_OR_REFERENCES_CHANGED"} {
		t.Run(code, func(t *testing.T) {
			f := commissionPG(t)
			old := commissionRetryBlocked(t, &f, code)
			preview := commissionRetryPreview(t, f, "known-unsent-retry")
			next, err := f.db.ApproveCommissionPreview(f.ctx, orders.CommissionApproval{Scope: old.Scope, PreviewID: preview.ID, Actor: f.actor, IdempotencyKey: "known-unsent-retry", RetryWorkID: old.ID}, time.Now())
			if err != nil || next.ID == old.ID || next.DispatchCount != 0 || next.Plan.ExternalCode == old.Plan.ExternalCode {
				t.Fatal("reviewed known-unsent retry unavailable", next, err)
			}
			commissionRetryAssertHead(t, f, next.ID, 2)
		})
	}
}

func TestCommissionPostgresRetryRejectsEarlierNativeConflictBehindNewHead(t *testing.T) {
	f := commissionPG(t)
	old := commissionRetryBlocked(t, &f, "FINANCE_EXISTING_DOCUMENT_UNVERIFIED")
	preview := commissionRetryPreview(t, f, "legacy-unsafe-retry")
	tx, err := f.db.Pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTransaction(tx)
	// Reproduce a historical head created before the native-conflict retry fence.
	legacy, err := insertCommissionWork(f.ctx, tx, orders.CommissionApproval{Scope: old.Scope, Actor: f.actor, IdempotencyKey: "legacy-unsafe-retry", RetryWorkID: old.ID}, preview, f.config.Finance.OwnerID, "legacy-fixture-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	head := commissionRetryBlocked(t, &f, "FINANCE_AUTHORITY_CHANGED")
	if head.ID != legacy.ID {
		t.Fatal("fixture did not claim newer head")
	}
	reviewed := commissionRetryPreview(t, f, "new-head-retry")
	_, err = f.db.ApproveCommissionPreview(f.ctx, orders.CommissionApproval{Scope: old.Scope, PreviewID: reviewed.ID, Actor: f.actor, IdempotencyKey: "new-head-retry", RetryWorkID: head.ID}, time.Now())
	if !errors.Is(err, orders.ErrBlocked) {
		t.Fatal("earlier native identity conflict lost behind new head", err)
	}
	commissionRetryAssertHead(t, f, head.ID, 2)
}
