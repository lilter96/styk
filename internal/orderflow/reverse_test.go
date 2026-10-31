package orderflow

import (
	"context"
	"slices"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestReverseSuggestionProposesOnlyReviewedTransitions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		stateID   string
		mapping   orders.ReverseStatusMapping
		status    string
		wantCode  string
		wantReady bool
	}{
		{"pack", "state-pack", orders.ReverseStatusMapping{StateID: "state-pack", Action: orders.ActionPostingPack}, "awaiting_packaging", "", true},
		{"ship", "state-ship", orders.ReverseStatusMapping{StateID: "state-ship", Action: orders.ActionPostingShip}, "awaiting_deliver", "", true},
		{"deliver", "state-deliver", orders.ReverseStatusMapping{StateID: "state-deliver", Action: orders.ActionPostingDeliver}, "driver_pickup", "", true},
		{"cancel", "state-cancel", orders.ReverseStatusMapping{StateID: "state-cancel", Action: orders.ActionPostingCancel}, "awaiting_deliver", "", true},
		{"echo", "state-pack", orders.ReverseStatusMapping{StateID: "state-pack", Action: orders.ActionPostingPack}, "awaiting_deliver", "reverse_action_already_applied", false},
		{"wrong-predecessor", "state-deliver", orders.ReverseStatusMapping{StateID: "state-deliver", Action: orders.ActionPostingDeliver}, "awaiting_deliver", "reverse_transition_not_available", false},
		{"unmapped", "state-other", orders.ReverseStatusMapping{StateID: "state-pack", Action: orders.ActionPostingPack}, "awaiting_packaging", "reverse_mapping_missing", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			review := flowReview(t, "posting")
			review.Config.ReverseStatusMappings = []orders.ReverseStatusMapping{test.mapping}
			correlation := review.Correlations[0]
			correlation.CustomerOrderID = "customer-order"
			posting := review.Postings[0]
			posting.Status = orders.Present(test.status)
			target := orders.TargetObservation{Scope: review.Config.Scope, Correlation: correlation, Revision: "target-revision",
				Fields: orders.CustomerOrderWrite{StateID: test.stateID}, Complete: true}
			store := &flowStore{review: review}
			service := flowService(t, store, flowResolver{source: &flowProvider{posting: posting}, target: &flowProvider{target: target}})
			suggestion, err := service.ReverseSuggestion(context.Background(), orders.ReverseSuggestionRequest{Scope: review.Config.Scope, PostingNumber: "posting"})
			if err != nil {
				t.Fatal(err)
			}
			if suggestion.Ready != test.wantReady || suggestion.MoySkladStateID != test.stateID || suggestion.CustomerOrderID != "customer-order" {
				t.Fatalf("suggestion=%+v", suggestion)
			}
			if test.wantCode != "" && !slices.ContainsFunc(suggestion.Blockers, func(blocker orders.Validation) bool { return blocker.Code == test.wantCode }) {
				t.Fatalf("blockers=%+v", suggestion.Blockers)
			}
			if test.wantReady {
				if suggestion.ProposedAction != test.mapping.Action || len(suggestion.ParametersRequired) == 0 || suggestion.ExpectedStatus == "" {
					t.Fatalf("ready suggestion lost its reviewed evidence: %+v", suggestion)
				}
				if suggestion.PostingStatus.Value == nil || *suggestion.PostingStatus.Value != test.status {
					t.Fatal("suggestion did not report the fresh provider status")
				}
			}
		})
	}
}

func TestReverseSuggestionRequiresLinkedCorrelation(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	review.Correlations = nil
	service := flowService(t, &flowStore{review: review}, flowResolver{source: &flowProvider{}, target: &flowProvider{}})
	if _, err := service.ReverseSuggestion(context.Background(), orders.ReverseSuggestionRequest{Scope: review.Config.Scope, PostingNumber: "posting"}); err != orders.ErrNotFound {
		t.Fatalf("err=%v", err)
	}
}

func TestReverseSuggestionRejectsStaleProviderIdentity(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	review.Config.ReverseStatusMappings = []orders.ReverseStatusMapping{{StateID: "state", Action: orders.ActionPostingPack}}
	posting := review.Postings[0]
	posting.PostingNumber = "other-posting"
	service := flowService(t, &flowStore{review: review}, flowResolver{source: &flowProvider{posting: posting}, target: &flowProvider{}})
	if _, err := service.ReverseSuggestion(context.Background(), orders.ReverseSuggestionRequest{Scope: review.Config.Scope, PostingNumber: "posting"}); err != orders.ErrStale {
		t.Fatalf("err=%v", err)
	}
}

func TestReverseSuggestionDoesNotWriteOrDispatch(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	review.Config.ReverseStatusMappings = []orders.ReverseStatusMapping{{StateID: "state", Action: orders.ActionPostingPack}}
	correlation := review.Correlations[0]
	correlation.CustomerOrderID = "customer-order"
	posting := review.Postings[0]
	posting.Status = orders.Present("awaiting_packaging")
	target := orders.TargetObservation{Scope: review.Config.Scope, Correlation: correlation, Revision: "revision",
		Fields: orders.CustomerOrderWrite{StateID: "state"}, Complete: true}
	source, destination := &flowProvider{posting: posting}, &flowProvider{target: target}
	service := flowService(t, &flowStore{review: review}, flowResolver{source: source, target: destination})
	if _, err := service.ReverseSuggestion(context.Background(), orders.ReverseSuggestionRequest{Scope: review.Config.Scope, PostingNumber: "posting"}); err != nil {
		t.Fatal(err)
	}
	if source.writes != 0 || destination.writes != 0 {
		t.Fatal("reverse suggestion performed an external write")
	}
}
