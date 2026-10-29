package catalog

import (
	"errors"
	"testing"
)

func TestBuildPlanRequiresConfirmedIdentifiers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*PlanRequest)
		want   error
	}{
		{"unconfirmed even with same name", func(r *PlanRequest) { r.Mapping.Confirmed = false }, ErrUnconfirmedMapping},
		{"wrong source id", func(r *PlanRequest) { r.Source.Identity.ID = "another" }, ErrIdentityMismatch},
		{"wrong source account", func(r *PlanRequest) { r.Source.Identity.Account.ID = "another" }, ErrIdentityMismatch},
		{"offer id is a separate namespace", func(r *PlanRequest) { r.Source.Identity.OfferID = "mismatch" }, ErrIdentityMismatch},
		{"invalid authority", func(r *PlanRequest) { r.Policies[FieldName] = "BOTH" }, ErrInvalidPolicy},
		{"invalid field", func(r *PlanRequest) { r.Policies["made_up"] = Ignore }, ErrInvalidPolicy},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := planFixture()
			tc.change(&request)
			if _, err := BuildPlan(request); !errors.Is(err, tc.want) {
				t.Fatalf("wanted %v, got %v", tc.want, err)
			}
		})
	}
}

func TestBuildPlanSelectsFieldAuthorityAndConverges(t *testing.T) {
	t.Parallel()
	request := planFixture()
	request.Policies[FieldDescription] = OzonAuthoritative
	request.Source.Fields[FieldName] = Text("MS name")
	request.Target.Fields[FieldName] = Text("old Ozon name")
	request.Source.Fields[FieldDescription] = Text("old MS description")
	request.Target.Fields[FieldDescription] = Text("Ozon description")
	plan := mustPlan(t, request)
	if len(plan.Changes) != 2 || plan.Noop {
		t.Fatalf("expected one change per direction: %+v", plan)
	}
	for _, change := range plan.Changes {
		if change.Field == FieldName && change.Target.Account.Provider != ProviderOzon {
			t.Fatal("name did not flow from MoySklad")
		}
		if change.Field == FieldDescription && change.Target.Account.Provider != ProviderMoySklad {
			t.Fatal("description did not flow from Ozon")
		}
	}
	request.Target.Fields[FieldName] = request.Source.Fields[FieldName]
	request.Source.Fields[FieldDescription] = request.Target.Fields[FieldDescription]
	if next := mustPlan(t, request); !next.Noop || len(next.Changes) != 0 {
		t.Fatalf("readback did not converge: %+v", next)
	}
}

func TestBuildPlanNeverInfersClearFromMissingObservation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		value  Value
		reason string
	}{
		{"empty", Empty(), "explicit_clear_required"},
		{"absent", Value{State: StateAbsent}, "source_absent"},
		{"unknown", Value{State: StateUnknown}, "source_unknown"},
		{"unsupported", Value{State: StateUnsupported}, "source_unsupported"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := descriptionFixture()
			request.Source.Fields[FieldDescription] = tc.value
			plan := mustPlan(t, request)
			if !plan.Noop || len(plan.Skipped) != 1 || plan.Skipped[0].Reason != tc.reason {
				t.Fatalf("unsafe observation produced write: %+v", plan)
			}
		})
	}
}

func TestBuildPlanExplicitClearReadbackDoesNotLoop(t *testing.T) {
	t.Parallel()
	request := descriptionFixture()
	request.Source.Fields[FieldDescription] = Clear()
	plan := mustPlan(t, request)
	if len(plan.Changes) != 1 || plan.Changes[0].After.State != StateClear {
		t.Fatalf("explicit clear not retained: %+v", plan)
	}
	request.Target.Fields[FieldDescription] = Empty()
	if plan = mustPlan(t, request); !plan.Noop {
		t.Fatalf("clear repeated after empty readback: %+v", plan)
	}
}

func TestBuildPlanThreeWayBaseline(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, source, target string
		changes, conflicts   int
		noop                 bool
	}{
		{"unchanged", "baseline", "baseline", 0, 0, true},
		{"source changed", "source edit", "baseline", 1, 0, false},
		{"target drift", "baseline", "target edit", 1, 0, false},
		{"both changed", "source edit", "target edit", 0, 1, false},
		{"both converged", "same edit", "same edit", 0, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := planFixture()
			baseline := fingerprint(t, Text("baseline"))
			request.Baseline = &Baseline{Source: map[Field]string{FieldName: baseline}, Target: map[Field]string{FieldName: baseline}}
			request.Source.Fields[FieldName], request.Target.Fields[FieldName] = Text(tc.source), Text(tc.target)
			plan := mustPlan(t, request)
			if len(plan.Changes) != tc.changes || len(plan.Conflicts) != tc.conflicts || plan.Noop != tc.noop {
				t.Fatalf("unexpected three-way result: %+v", plan)
			}
		})
	}
}

func TestBuildPlanManualAndUnsupportedFacts(t *testing.T) {
	t.Parallel()
	request := descriptionFixture()
	request.Policies[FieldDescription] = Manual
	if plan := mustPlan(t, request); len(plan.Skipped) != 1 || plan.Skipped[0].Reason != "manual_value_required" {
		t.Fatalf("manual field inferred an automatic source: %+v", plan)
	}
	request.ManualValues = map[Field]Value{FieldDescription: Text("reviewed text")}
	if plan := mustPlan(t, request); len(plan.Changes) != 1 || !EqualValue(plan.Changes[0].After, request.ManualValues[FieldDescription]) {
		t.Fatalf("manual text was not selected: %+v", plan)
	}
	request.Policies[FieldPrice] = MoySkladAuthoritative
	if plan := mustPlan(t, request); len(plan.Skipped) != 1 || plan.Skipped[0].Reason != "unsupported_write_field" {
		t.Fatalf("unsupported ordinary price update was not explicit: %+v", plan)
	}
}

func TestBuildPlanRejectsInvalidTextAndUnclearableName(t *testing.T) {
	t.Parallel()
	for _, value := range []Value{Clear(), Text("  "), {State: StateKnown, Data: []byte(`42`)}} {
		request := planFixture()
		request.Source.Fields[FieldName] = value
		if _, err := BuildPlan(request); err == nil {
			t.Fatalf("invalid name accepted: %+v", value)
		}
	}
}

func planFixture() PlanRequest {
	source := Identity{Account: Account{Provider: ProviderMoySklad, ID: "ms-account"}, EntityType: "product", ID: "ms-id", OfferID: "separate-offer"}
	target := Identity{Account: Account{Provider: ProviderOzon, ID: "ozon-account"}, EntityType: "product", ID: "ozon-id", OfferID: "offer-1", SKU: "sku-1"}
	return PlanRequest{Mapping: Mapping{ID: "confirmed-pair", Version: 1, Source: source, Target: target, Confirmed: true},
		Source:   Product{Identity: source, Fields: map[Field]Value{FieldName: Text("same name")}},
		Target:   Product{Identity: target, Fields: map[Field]Value{FieldName: Text("same name")}},
		Policies: Policies{FieldName: MoySkladAuthoritative}}
}

func descriptionFixture() PlanRequest {
	request := planFixture()
	request.Policies = Policies{FieldDescription: MoySkladAuthoritative}
	request.Source.Fields[FieldDescription] = Text("source description")
	request.Target.Fields[FieldDescription] = Text("target description")
	return request
}

func mustPlan(t *testing.T, request PlanRequest) Plan {
	t.Helper()
	plan, err := BuildPlan(request)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
