package orderflow

import (
	"slices"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func flowStateBaseline(target orders.TargetObservation) orders.NativeStateBaseline {
	return orders.NativeStateBaseline{Scope: target.Scope, Correlation: target.Correlation, WorkID: "own-state-work", CommandID: "own-state-command",
		StateID: target.Fields.StateID, TargetRevision: target.Revision, ObservedAt: target.ObservedAt}
}

func TestMirrorStateProjectionRequiresOwnUnchangedRevision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*orders.NativeStateBaseline, *orders.TargetObservation)
		state  bool
	}{
		{"own unchanged revision", func(*orders.NativeStateBaseline, *orders.TargetObservation) {}, true},
		{"missing provenance", func(b *orders.NativeStateBaseline, _ *orders.TargetObservation) { *b = orders.NativeStateBaseline{} }, false},
		{"merchant transition", func(_ *orders.NativeStateBaseline, target *orders.TargetObservation) {
			target.Fields.StateID = "merchant-ready"
		}, false},
		{"revision changed with same state", func(_ *orders.NativeStateBaseline, target *orders.TargetObservation) {
			target.Revision = "merchant-new-revision"
		}, false},
		{"foreign native order", func(b *orders.NativeStateBaseline, _ *orders.TargetObservation) {
			b.Correlation.CustomerOrderID = "foreign-order"
		}, false},
		{"foreign account", func(b *orders.NativeStateBaseline, _ *orders.TargetObservation) {
			b.Scope.OzonAccount = "foreign-account"
		}, false},
		{"older read", func(b *orders.NativeStateBaseline, _ *orders.TargetObservation) {
			b.ObservedAt = b.ObservedAt.Add(time.Second)
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			review, target := existingFlowReview(t)
			target.Fields.StateID = "own-previous-state"
			baseline := flowStateBaseline(target)
			tc.change(&baseline, &target)
			review.NativeStateBaselines = map[string]orders.NativeStateBaseline{target.Correlation.PostingNumber: baseline}
			target.Fields.Name = orders.Present("merchant-name")
			preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{target: &flowProvider{target: target}}), review)
			decision := preview.Decisions[0]
			if !decision.Ready || len(decision.Commands) != 1 {
				t.Fatalf("independent metadata update blocked: %+v", decision)
			}
			command := decision.Commands[0]
			if slices.Contains(command.CustomerOrder.OwnedFields, "state") != tc.state || (command.NativeStateBaseline != nil) != tc.state {
				t.Fatalf("state provenance not enforced: %+v", command)
			}
			if !tc.state && command.CustomerOrder.StateID != target.Fields.StateID {
				t.Fatal("merchant state was erased by forward projection")
			}
		})
	}
}

func TestRunnerMerchantTransitionAfterMirrorReviewStopsWrite(t *testing.T) {
	t.Parallel()
	review, target := existingFlowReview(t)
	target.Fields.StateID = "own-previous-state"
	review.NativeStateBaselines = map[string]orders.NativeStateBaseline{target.Correlation.PostingNumber: flowStateBaseline(target)}
	store := &flowStore{review: review}
	provider := &flowProvider{target: target, correlation: target.Correlation}
	resolver := flowResolver{source: &flowProvider{posting: review.Postings[0]}, target: provider}
	service := flowService(t, store, resolver)
	preview := flowPreview(t, service, review)
	work := flowWork(t, preview)
	store.work = &work
	provider.target.Fields.StateID = "merchant-ready"
	runFlow(t, flowRunner(t, store, resolver, service))
	if store.work.State != orders.StateBlocked || provider.writes != 0 || provider.target.Fields.StateID != "merchant-ready" {
		t.Fatalf("concurrent merchant intent lost: state=%s writes=%d", store.work.State, provider.writes)
	}
}

func TestPendingMerchantTransitionFencesMetadataWithoutRebasingIntent(t *testing.T) {
	t.Parallel()
	review, target := existingFlowReview(t)
	review.MerchantPending = map[string]bool{review.Postings[0].PostingNumber: true}
	preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{target: &flowProvider{target: target}}), review)
	decision := preview.Decisions[0]
	if decision.Ready || len(decision.Commands) != 0 || !slices.ContainsFunc(decision.Validation, func(v orders.Validation) bool { return v.Code == "merchant_transition_pending" }) {
		t.Fatal("metadata mirror could erase native event revision", decision)
	}
}

func TestMoyskladPrimaryLifecycleNeverWritesExistingNativeState(t *testing.T) {
	t.Parallel()
	review, target := existingFlowReview(t)
	target.Fields.StateID = "own-previous-state"
	review.NativeStateBaselines = map[string]orders.NativeStateBaseline{target.Correlation.PostingNumber: flowStateBaseline(target)}
	review.Config.Lifecycle = &orders.LifecyclePolicy{ID: "00000000-0000-4000-8000-000000000011", Scope: review.Config.Scope, OwnerID: "owner", Enabled: true, Revision: 1, ConfigRevision: review.Config.Revision, GrantedAt: flowTestNow.Add(-time.Hour), DemandMoment: orders.OnHandoffConfirmed, Mappings: []orders.MerchantCommandMapping{{StateID: "00000000-0000-4000-8000-000000000012", FromStateIDs: []string{"00000000-0000-4000-8000-000000000013"}, Scheme: "FBS", Action: orders.ActionPostingPack, EventKind: "assembly_ready", WholePosting: true}}}
	target.Fields.Name = orders.Present("merchant-name")
	preview := flowPreview(t, flowService(t, &flowStore{review: review}, flowResolver{target: &flowProvider{target: target}}), review)
	decision := preview.Decisions[0]
	if !decision.Ready || len(decision.Commands) != 1 {
		t.Fatal("metadata update blocked", decision)
	}
	command := decision.Commands[0]
	if command.NativeStateBaseline != nil || slices.Contains(command.CustomerOrder.OwnedFields, "state") || command.CustomerOrder.StateID != target.Fields.StateID {
		t.Fatal("MS-primary mode emitted state PUT", command)
	}
}
