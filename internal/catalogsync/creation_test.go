package catalogsync

import (
	"testing"

	"github.com/lilter96/styk/internal/catalog"
)

func makeCreation(f *fixture, provider catalog.Provider) *memoryProvider {
	enrollment := &f.store.current.Config.Targets[0]
	enrollment.MappingID, enrollment.MappingVersion = "", 0
	writer := f.target
	if provider == catalog.ProviderMoySklad {
		enrollment.Source, enrollment.Target = enrollment.Target, enrollment.Source
		writer = f.source
	}
	enrollment.Target.ID, enrollment.Target.OfferID, enrollment.Target.SKU = "", "new-offer", ""
	if provider == catalog.ProviderMoySklad {
		enrollment.Target.OfferID = ""
	}
	enrollment.Create = &Creation{ID: "b0000000-0000-4000-8000-000000000001", ExternalCode: "STYK-CATALOG-MARKER", Values: map[catalog.Field]catalog.Value{
		catalog.FieldName: catalog.Text("created name"), catalog.FieldDescription: catalog.Text("created description")}}
	f.store.current.Targets[0] = TargetContext{Enrollment: clone(*enrollment)}
	return writer
}

func TestCreationUsesPersistedProviderIdentityAfterRestart(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider catalog.Provider
	}{
		{name: "MoySklad", provider: catalog.ProviderMoySklad},
		{name: "Ozon", provider: catalog.ProviderOzon},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			writer := makeCreation(f, tc.provider)
			f.approve(t)
			f.run(t)
			if created := f.store.works[0].CreatedTarget; created == nil || created.ID == f.store.current.Config.Targets[0].Create.ID {
				t.Fatal("provider identity must be persisted independently of the sync ID")
			}
			f.runner.WorkerID = "creation-restart"
			f.run(t)
			if f.store.works[0].State != StateReconciled || f.store.current.Targets[0].CreatedTarget == nil {
				t.Fatal("created target was not durably reconciled")
			}
			if decision := f.preview(t).Decisions[0]; len(decision.Commands) != 0 || writer.writes != 1 {
				t.Fatal("persisted creation was offered for duplicate creation")
			}
		})
	}
}

func TestUnknownCreationResolvesExactMarkerWithoutResubmission(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	writer := makeCreation(f, catalog.ProviderMoySklad)
	writer.writeErr, writer.applyOnError = errTestProvider, true
	f.approve(t)
	f.run(t)
	if f.store.works[0].State != StateUnknown || f.store.works[0].CreatedTarget != nil {
		t.Fatal("unknown creation incorrectly acquired a provider identity")
	}
	f.run(t)
	if f.store.works[0].State != StateReconciled || f.store.current.Targets[0].CreatedTarget == nil || writer.writes != 1 {
		t.Fatal("exact marker reconciliation did not recover creation")
	}
}

func TestCreationPreviewRequiresProviderValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		validation  []catalog.FieldError
		unavailable bool
		code        string
	}{
		{name: "missing required category", validation: []catalog.FieldError{{Field: catalog.FieldCategory, Code: "CATEGORY_REQUIRED"}}, code: "CATEGORY_REQUIRED"},
		{name: "metadata unavailable", unavailable: true, code: "PROVIDER_VALIDATION_UNAVAILABLE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			writer := makeCreation(f, catalog.ProviderMoySklad)
			writer.validation = tc.validation
			if tc.unavailable {
				writer.validationErr = errTestProvider
			}
			decision := f.preview(t).Decisions[0]
			if decision.Ready || len(decision.Commands) != 0 || len(decision.Validation) != 1 || decision.Validation[0].Code != tc.code {
				t.Fatalf("creation validation did not block: %+v", decision.Validation)
			}
		})
	}
}

func TestCreationCannotBypassConfirmedMapping(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	mapping := f.store.current.Targets[0].Mapping
	makeCreation(f, catalog.ProviderMoySklad)
	f.store.current.Targets[0].Mapping = mapping
	decision := f.preview(t).Decisions[0]
	if decision.Ready || len(decision.Commands) != 0 || decision.Validation[0].Code != "CREATE_HAS_CONFIRMED_MAPPING" {
		t.Fatal("explicit creation bypassed the existing confirmed relationship")
	}
}
