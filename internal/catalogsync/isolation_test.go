package catalogsync

import (
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

func TestMixedAuthorityCommandsRebaseOnlyVerifiedSiblingEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.store.current.Config.Rules[catalog.FieldDescription] = catalog.OzonAuthoritative
	product := f.target.products["target-1"]
	product.Fields[catalog.FieldDescription] = catalog.Text("Ozon description")
	f.target.products[product.Identity.ID] = product
	operation := f.approve(t)
	if len(operation.Targets) != 2 || operation.Targets[0].Command.Target.Account == operation.Targets[1].Command.Target.Account {
		t.Fatal("mixed authorities must produce two exact single-target commands")
	}
	for range 4 {
		f.run(t)
	}
	if f.source.writes != 1 || f.target.writes != 1 || f.store.works[0].State != StateReconciled || f.store.works[1].State != StateReconciled {
		t.Fatalf("mixed directions: writes=%d/%d, states=%s/%s", f.source.writes, f.target.writes, f.store.works[0].State, f.store.works[1].State)
	}
	if decision := f.preview(t).Decisions[0]; !decision.Plan.Noop || len(decision.Commands) != 0 {
		t.Fatal("mixed authority synchronization would loop")
	}
}

func TestUnrelatedEditPreventsSiblingRebase(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.store.current.Config.Rules[catalog.FieldDescription] = catalog.OzonAuthoritative
	p := f.target.products["target-1"]
	p.Fields[catalog.FieldDescription] = catalog.Text("Ozon description")
	f.target.products[p.Identity.ID] = p
	f.approve(t)
	f.run(t)
	p = f.target.products["target-1"]
	p.Fields[catalog.FieldName] = catalog.Text("unreviewed name")
	f.target.products[p.Identity.ID] = p
	f.run(t)
	f.run(t)
	if f.store.works[1].State != StateBlocked || f.target.writes != 0 {
		t.Fatal("unreviewed changes were silently rebased into pending work")
	}
}

func TestSourceChangedAfterDispatchDoesNotAdvanceBaseline(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.approve(t)
	f.run(t)
	product := f.source.products["source-1"]
	product.Fields[catalog.FieldName] = catalog.Text("source changed during readback")
	product.Revision = "later-source-revision"
	f.source.products[product.Identity.ID] = product
	f.run(t)
	if f.store.works[0].State != StateBlocked || f.store.works[0].Code != "SOURCE_CHANGED_AFTER_DISPATCH" || f.store.current.Targets[0].Baseline != nil {
		t.Fatal("source beyond approved evidence was incorrectly baselined")
	}
	if decision := f.preview(t).Decisions[0]; len(decision.Commands) != 1 || f.target.writes != 1 {
		t.Fatal("changed source must require a fresh review without another write")
	}
}

func addSecondEnrollment(f *fixture) {
	scope := f.store.current.Config.Scope
	source := testProduct(scope, catalog.ProviderMoySklad, "source-2", "second new name")
	target := testProduct(scope, catalog.ProviderOzon, "target-2", "second old name")
	f.source.products[source.Identity.ID], f.target.products[target.Identity.ID] = source, target
	mapping := catalog.Mapping{ID: "mapping-2", Version: 1, Source: source.Identity, Target: target.Identity, Confirmed: true}
	enrollment := Enrollment{ID: "enrollment-2", MappingID: mapping.ID, MappingVersion: mapping.Version, Source: source.Identity, Target: target.Identity}
	f.store.current.Config.Targets = append(f.store.current.Config.Targets, enrollment)
	f.store.current.Targets = append(f.store.current.Targets, TargetContext{Enrollment: enrollment, Mapping: &mapping})
}

func TestUnknownTargetDoesNotFenceOtherTargets(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	addSecondEnrollment(f)
	f.approve(t)
	f.target.writeErr = errTestProvider
	f.run(t)
	if f.store.works[0].State != StateUnknown {
		t.Fatal("first target should be uncertain")
	}
	f.target.writeErr = nil
	worked, err := f.runner.RunNext(f.ctx)
	if err != nil || !worked || f.store.works[1].State != StateAcknowledged || f.target.writes != 2 {
		t.Fatalf("unrelated target was fenced: worked=%v error=%v", worked, err)
	}
	if f.target.commands[1].Target.ID != "target-2" || len(f.target.commands[1].Values) != 1 {
		t.Fatal("runner widened the second target's exact intent")
	}
}

func TestRunnerPreservesDefiniteRejectedReceiptWithError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.approve(t)
	f.target.writeErr = errTestProvider
	f.target.writeReceipt = &catalog.Receipt{State: catalog.ReceiptRejected, HTTPStatus: 409,
		Errors: []catalog.FieldError{{Field: catalog.FieldName, Code: "INVALID_VALUE", Message: "untrusted provider body must not persist"}}}
	f.run(t)
	work := f.store.works[0]
	if work.State != StateRejected || work.Receipt.HTTPStatus != 409 || len(work.Receipt.Errors) != 1 || work.Receipt.Errors[0].Message != "" {
		t.Fatalf("known rejected receipt lost or leaked: %+v", work.Receipt)
	}
	f.now = f.now.Add(time.Minute)
	worked, err := f.runner.RunNext(f.ctx)
	if err != nil || worked || f.target.writes != 1 {
		t.Fatal("definite rejection was resubmitted")
	}
}

func TestAsyncPartialReceiptPersistsAndNeverResubmits(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.target.accept = true
	f.approve(t)
	f.run(t)
	f.target.pollReceipt = &catalog.Receipt{State: catalog.ReceiptPartial, HTTPStatus: 200,
		Errors: []catalog.FieldError{{Field: catalog.FieldName, Code: "INVALID_VALUE"}}}
	f.run(t)
	work := f.store.works[0]
	if work.State != StateUnknown || work.Receipt.TaskID != "task-1" || len(work.Receipt.Errors) != 1 || work.Receipt.HTTPStatus != 200 {
		t.Fatalf("partial task receipt was discarded: %+v", work.Receipt)
	}
	f.run(t)
	if f.target.writes != 1 || f.store.works[0].State != StateUnknown {
		t.Fatal("partially applied task was resubmitted or incorrectly reconciled")
	}
}
