//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/identity"
)

func TestCatalogManualReadbackRequiresActorScopeAndExplicitConsent(t *testing.T) {
	t.Parallel()
	db, work := catalogManualUnknown(t)
	before := catalogManualFreeze(t, db, work)
	tests := []struct {
		name   string
		change func(*catalogsync.ReconciliationRequest)
	}{
		{"missing actor", func(r *catalogsync.ReconciliationRequest) { r.Actor = "" }},
		{"foreign tenant", func(r *catalogsync.ReconciliationRequest) { r.Scope.Tenant = "foreign" }},
		{"foreign source account", func(r *catalogsync.ReconciliationRequest) { r.Scope.MoyskladAccount = "foreign" }},
		{"foreign target account", func(r *catalogsync.ReconciliationRequest) { r.Scope.OzonAccount = "foreign" }},
		{"foreign work", func(r *catalogsync.ReconciliationRequest) { r.WorkID = "foreign-work" }},
		{"unacknowledged", func(r *catalogsync.ReconciliationRequest) { r.Acknowledge = false }},
		{"missing key", func(r *catalogsync.ReconciliationRequest) { r.IdempotencyKey = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := catalogManualRequest(work, "invalid-request")
			tc.change(&req)
			if _, err := db.BeginCatalogReconciliation(t.Context(), req, time.Now()); err == nil {
				t.Fatal("invalid authority created readback intent")
			}
		})
	}
	ctx := identity.WithSession(t.Context(), identity.Session{AuditID: "user.foreign-session"})
	if _, err := db.BeginCatalogReconciliation(ctx, catalogManualRequest(work, "actor-substitution"), time.Now()); err == nil {
		t.Fatal("session actor substitution accepted")
	}
	var count int
	if err := db.Pool.QueryRow(t.Context(), `SELECT count(*) FROM catalog_sync_readbacks`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unauthorized readback persisted: %d %v", count, err)
	}
	catalogManualAssertFrozen(t, db, work, before)
}

func TestCatalogManualReadbackConcurrentClaimCompletedReplayAndImmutableAudit(t *testing.T) {
	t.Parallel()
	db, work := catalogManualUnknown(t)
	before := catalogManualFreeze(t, db, work)
	winner, req := catalogManualConcurrentClaim(t, db, catalogManualRequest(work, "concurrent-readback"))
	completion := catalogManualCompletion(winner)
	result, err := db.CompleteCatalogReconciliation(t.Context(), winner, completion)
	if err != nil || !result.Completed || result.State != catalogsync.StateUnknown {
		t.Fatalf("complete read-only review: %+v %v", result, err)
	}
	replay, err := db.BeginCatalogReconciliation(t.Context(), req, time.Now())
	if err != nil || replay.Result == nil || *replay.Result != result {
		t.Fatalf("completed idempotent result lost: %+v %v", replay, err)
	}
	if _, err = db.CompleteCatalogReconciliation(t.Context(), winner, completion); err == nil {
		t.Fatal("deleted claim completed twice")
	}
	req.Reason = "Changed meaning with same key"
	if _, err = db.BeginCatalogReconciliation(t.Context(), req, time.Now()); !errors.Is(err, catalogsync.ErrConflict) {
		t.Fatalf("changed idempotent intent accepted: %v", err)
	}
	catalogManualAssertFrozen(t, db, work, before)
	catalogManualAuditConstraints(t, db.Pool, winner.ID)
}

func TestCatalogManualReadbackExpiredClaimRejectsLateCompletion(t *testing.T) {
	t.Parallel()
	db, work := catalogManualUnknown(t)
	before := catalogManualFreeze(t, db, work)
	old := catalogManualBegin(t, db, work, "old-request")
	if _, err := db.Pool.Exec(t.Context(), `UPDATE catalog_sync_readback_claims SET expires_at=clock_timestamp()-interval '1 second' WHERE request_id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CompleteCatalogReconciliation(t.Context(), old, catalogManualCompletion(old)); err == nil {
		t.Fatal("expired lease completed")
	}
	newer := catalogManualBegin(t, db, work, "replacement-read-only-request")
	if newer.Token == old.Token {
		t.Fatal("replacement claim reused old token")
	}
	if _, err := db.CompleteCatalogReconciliation(t.Context(), old, catalogManualCompletion(old)); err == nil {
		t.Fatal("late completion superseded newer readback")
	}
	replay, err := db.BeginCatalogReconciliation(t.Context(), catalogManualRequest(work, "old-request"), time.Now())
	if err != nil || replay.Result == nil || replay.Result.Code != "READBACK_LEASE_EXPIRED" {
		t.Fatalf("expired request audit lost: %+v %v", replay, err)
	}
	if _, err = db.CompleteCatalogReconciliation(t.Context(), newer, catalogManualCompletion(newer)); err != nil {
		t.Fatal(err)
	}
	catalogManualAssertFrozen(t, db, work, before)
}

func TestCatalogManualReadbackCompletionRejectsScopeWorkAndTokenSubstitution(t *testing.T) {
	t.Parallel()
	db, work := catalogManualUnknown(t)
	claim := catalogManualBegin(t, db, work, "completion-substitution")
	tests := []struct {
		name   string
		change func(*catalogsync.ReconciliationClaim, *catalogsync.Completion)
	}{
		{"tenant", func(c *catalogsync.ReconciliationClaim, r *catalogsync.Completion) {
			c.Work.Scope.Tenant = "foreign"
			r.Scope = c.Work.Scope
		}},
		{"account", func(c *catalogsync.ReconciliationClaim, r *catalogsync.Completion) {
			c.Work.Scope.OzonAccount = "foreign"
			r.Scope = c.Work.Scope
		}},
		{"work", func(c *catalogsync.ReconciliationClaim, r *catalogsync.Completion) {
			c.Work.ID = "foreign-work"
			r.WorkID = c.Work.ID
		}},
		{"token", func(c *catalogsync.ReconciliationClaim, r *catalogsync.Completion) {
			c.Token = strings.Repeat("f", 32)
			r.ClaimToken = c.Token
		}},
		{"request", func(c *catalogsync.ReconciliationClaim, _ *catalogsync.Completion) { c.ID = strings.Repeat("e", 32) }},
		{"provider task", func(_ *catalogsync.ReconciliationClaim, r *catalogsync.Completion) { r.Receipt.TaskID = "foreign-task" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			forged, c := claim, catalogManualCompletion(claim)
			tc.change(&forged, &c)
			if _, err := db.CompleteCatalogReconciliation(t.Context(), forged, c); err == nil {
				t.Fatal("substituted completion accepted")
			}
		})
	}
	if _, err := db.CompleteCatalogReconciliation(t.Context(), claim, catalogManualCompletion(claim)); err != nil {
		t.Fatalf("rejected substitutions corrupted valid claim: %v", err)
	}
}

func TestCatalogManualReadbackReconcilerPreservesWarningsWithoutAnotherWrite(t *testing.T) {
	t.Parallel()
	db, work := catalogManualUnknown(t)
	before := catalogManualFreeze(t, db, work)
	runner, source, target := catalogManualReconciler(db, work)
	req := catalogManualRequest(work, "accepted-warnings")
	result, err := runner.Reconcile(t.Context(), req)
	if err != nil || !result.Completed || result.State != catalogsync.StateReconciled || result.Code != "READBACK_MATCHED_WITH_WARNINGS" {
		t.Fatalf("read-only task reconciliation: %+v %v", result, err)
	}
	actual := catalogManualHistory(t, db, work.Scope)
	if actual.State != catalogsync.StateReconciled || catalogManualJSON(t, actual.Receipt) != catalogManualJSON(t, work.Receipt) {
		t.Fatal("verified fields or partial provider diagnostics were lost")
	}
	if target.polls != 1 || target.reads != 1 || source.reads != 1 || target.writes != 0 || source.writes != 0 {
		t.Fatalf("unexpected provider calls: poll=%d read=%d/%d write=%d/%d", target.polls, source.reads, target.reads, source.writes, target.writes)
	}
	replay, err := runner.Reconcile(t.Context(), req)
	if err != nil || replay != result || target.polls != 1 || target.writes != 0 {
		t.Fatalf("completed request replay called provider: %+v %v", replay, err)
	}
	if next, err := db.ClaimCatalogWork(t.Context(), catalogsync.ClaimRequest{WorkerID: "no-retry", Now: time.Now(), LeaseUntil: time.Now().Add(time.Minute)}); err != nil || next != nil {
		t.Fatalf("reconciled original task became claimable: %+v %v", next, err)
	}
	catalogManualAssertFrozen(t, db, work, before)
}

func TestCatalogManualReadbackDoesNotResolveUnacceptedOrUnsafeDiagnostics(t *testing.T) {
	t.Parallel()
	tests := []string{"not accepted", "hard error", "unknown severity", "unapproved warning code", "not imported", "changed task", "missing task", "selected field mismatch", "changed source"}
	for _, kind := range tests {
		t.Run(kind, func(t *testing.T) {
			db, work := catalogManualUnknownReceipt(t, func(receipt *catalog.Receipt) {
				if kind == "missing task" {
					receipt.TaskID = ""
				}
			})
			runner, source, target := catalogManualReconciler(db, work)
			req := catalogManualRequest(work, "diagnostics-review")
			catalogManualUnsafeTask(kind, &req, target)
			if kind == "changed source" {
				source.product.Fields = map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Unreviewed source edit")}
			}
			result, err := runner.Reconcile(t.Context(), req)
			if err != nil || result.State != catalogsync.StateUnknown || !result.Completed {
				t.Fatalf("unsafe task resolved: %+v %v", result, err)
			}
			if actual := catalogManualHistory(t, db, work.Scope); actual.State != catalogsync.StateUnknown {
				t.Fatal("unsafe result removed uncertainty")
			}
			if target.writes != 0 || source.writes != 0 {
				t.Fatal("diagnostic review invoked writer")
			}
		})
	}
}

func catalogManualUnsafeTask(kind string, req *catalogsync.ReconciliationRequest, target *catalogManualProvider) {
	switch kind {
	case "not accepted":
		req.AcceptWarnings = false
	case "hard error":
		target.receipt.Errors[0].Severity = "error"
	case "unknown severity":
		target.receipt.Products[0].Errors[0].Severity = ""
	case "unapproved warning code":
		target.receipt.Products[0].Errors[0].Code = "unapproved_warning"
	case "not imported":
		target.receipt.Products[0].Status = "processing"
	case "changed task":
		target.receipt.TaskID = "another-provider-task"
	case "selected field mismatch":
		target.product.Fields = nil
	}
}

type catalogManualSQL interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func catalogManualAuditConstraints(t *testing.T, db catalogManualSQL, id string) {
	t.Helper()
	statements := []struct{ name, sql, code string }{
		{"request update", `UPDATE catalog_sync_readbacks SET reason='rewritten audit' WHERE id=$1`, "55000"},
		{"request delete", `DELETE FROM catalog_sync_readbacks WHERE id=$1`, "55000"},
		{"result update", `UPDATE catalog_sync_readback_results SET completed_at=clock_timestamp() WHERE request_id=$1`, "55000"},
		{"result delete", `DELETE FROM catalog_sync_readback_results WHERE request_id=$1`, "55000"},
		{"claim FK", `INSERT INTO catalog_sync_readback_claims(work_id,request_id,token,expires_at) VALUES('foreign-work',$1,repeat('f',32),clock_timestamp()+interval '1 minute')`, "23503"},
		{"result FK", `INSERT INTO catalog_sync_readback_results(request_id,work_id,result,completed_at) VALUES(repeat('f',32),'foreign-work',jsonb_build_object('id',repeat('f',32),'work_id','foreign-work','state','UNKNOWN','code','SYNTHETIC','completed',true),clock_timestamp())`, "23503"},
	}
	for _, tc := range statements {
		t.Run(tc.name, func(t *testing.T) {
			var args []any
			if strings.Contains(tc.sql, "$1") {
				args = []any{id}
			}
			_, err := db.Exec(t.Context(), tc.sql, args...)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != tc.code {
				t.Fatalf("audit constraint code=%s: %v", tc.code, err)
			}
		})
	}
}
