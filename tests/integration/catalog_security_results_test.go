//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
)

func TestCatalogSecurityAsyncResultCannotSubstituteReviewedTarget(t *testing.T) {
	tests := []struct {
		name   string
		change func(*catalog.Receipt)
	}{
		{name: "foreign_task", change: func(r *catalog.Receipt) { r.TaskID = "999" }},
		{name: "foreign_command", change: func(r *catalog.Receipt) { r.CommandID = "foreign-command" }},
		{name: "foreign_product", change: func(r *catalog.Receipt) { r.Products[0].ID = "999" }},
		{name: "foreign_offer", change: func(r *catalog.Receipt) { r.Products[0].OfferID = "FOREIGN-OFFER" }},
		{name: "foreign_external_id", change: func(r *catalog.Receipt) { r.ExternalID = "999" }},
		{name: "extra_product", change: func(r *catalog.Receipt) { r.Products = append(r.Products, r.Products[0]) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newCatalogSecurityWorkflow(t)
			f.approve(t)
			f.run(t)
			if f.target.writes != 1 || f.work(t).State != catalogsync.StateProcessing {
				t.Fatal("async fixture never reached one accepted provider dispatch")
			}
			f.target.poll = func(command catalog.Command, accepted catalog.Receipt) (catalog.Receipt, error) {
				result := catalogSecurityAccepted(command)
				result.State, result.TaskID, result.ExternalID = catalog.ReceiptApplied, accepted.TaskID, command.Target.ID
				tc.change(&result)
				return result, nil
			}
			f.now = f.now.Add(10 * time.Second)
			f.run(t)
			work := f.work(t)
			if work.State != catalogsync.StateProcessing || f.target.writes != 1 || f.target.polls != 1 {
				t.Fatalf("foreign async result advanced or resent the command: state=%s writes=%d polls=%d", work.State, f.target.writes, f.target.polls)
			}
		})
	}
}

func TestCatalogSecurityDispatchRejectsChangedReviewedFacts(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, *catalogSecurityWorkflow)
	}{
		{name: "source_value", change: func(_ *testing.T, f *catalogSecurityWorkflow) {
			f.source.product.Fields[catalog.FieldName] = catalog.Text("Unreviewed source edit")
		}},
		{name: "target_value", change: func(_ *testing.T, f *catalogSecurityWorkflow) {
			f.target.product.Fields[catalog.FieldName] = catalog.Text("Unreviewed target edit")
		}},
		{name: "source_revision", change: func(_ *testing.T, f *catalogSecurityWorkflow) { f.source.product.Revision = "unreviewed-source" }},
		{name: "target_revision", change: func(_ *testing.T, f *catalogSecurityWorkflow) { f.target.product.Revision = "unreviewed-target" }},
		{name: "credential_rotation", change: func(t *testing.T, f *catalogSecurityWorkflow) {
			f.saveCredential(t, f.target.product.Identity.Account, 1)
		}},
		{name: "config_revision", change: catalogSecurityChangeConfig},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newCatalogSecurityWorkflow(t)
			f.approve(t)
			tc.change(t, f)
			f.run(t)
			if work := f.work(t); f.target.writes != 0 || work.State != catalogsync.StateBlocked {
				t.Fatalf("stale reviewed facts reached provider dispatch: writes=%d state=%s", f.target.writes, work.State)
			}
		})
	}
}

func catalogSecurityChangeConfig(t *testing.T, f *catalogSecurityWorkflow) {
	t.Helper()
	request := catalogsync.SaveConfigRequest{Config: f.config, ExpectedRevision: f.config.Revision, Actor: "synthetic-security-actor", IdempotencyKey: "security-changed-config"}
	request.Config.IntervalSeconds++
	if _, err := f.service.SaveConfig(t.Context(), request); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogSecurityApprovalRejectsStaleEvidenceAtomically(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, *catalogSecurityWorkflow)
	}{
		{name: "credentials", change: func(t *testing.T, f *catalogSecurityWorkflow) {
			f.saveCredential(t, f.target.product.Identity.Account, 1)
		}},
		{name: "configuration", change: catalogSecurityChangeConfig},
		{name: "expiry", change: func(_ *testing.T, f *catalogSecurityWorkflow) { f.now = f.now.Add(6 * time.Minute) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newCatalogSecurityWorkflow(t)
			preview := f.preview(t)
			tc.change(t, f)
			approval := catalogsync.Approval{Scope: f.scope, PreviewID: preview.ID, Actor: "synthetic-security-actor", IdempotencyKey: "stale-security-approval"}
			if operation, err := f.service.Approve(t.Context(), approval); err == nil || operation.ID != "" {
				t.Fatal("stale evidence produced an approved operation")
			}
			var operations, tasks int
			err := f.db.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM catalog_sync_operations),(SELECT count(*) FROM outbox_tasks WHERE task_type='catalog_sync')`).Scan(&operations, &tasks)
			if err != nil || operations != 0 || tasks != 0 || f.target.writes != 0 {
				t.Fatalf("stale approval left partial effects: operations=%d tasks=%d writes=%d error=%v", operations, tasks, f.target.writes, err)
			}
		})
	}
}

func TestCatalogSecurityPreviewRejectsStaleMappingBeforeSnapshots(t *testing.T) {
	f := newCatalogSecurityWorkflow(t)
	preview := f.preview(t)
	preview.ID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	preview.Decisions[0].Mapping.Version++
	if _, err := f.db.SaveCatalogPreview(t.Context(), preview); !errors.Is(err, catalogsync.ErrStale) {
		t.Fatalf("stale confirmed mapping was not enforced: %v", err)
	}
	var previews, snapshots, tasks int
	err := f.db.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM catalog_sync_previews),(SELECT count(*) FROM catalog_sync_snapshots),(SELECT count(*) FROM outbox_tasks WHERE task_type='catalog_sync')`).Scan(&previews, &snapshots, &tasks)
	if err != nil || previews != 1 || snapshots != 2 || tasks != 0 {
		t.Fatalf("stale mapping left partial review evidence: previews=%d snapshots=%d tasks=%d error=%v", previews, snapshots, tasks, err)
	}
}
