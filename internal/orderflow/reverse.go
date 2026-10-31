package orderflow

import (
	"context"
	"fmt"

	"github.com/lilter96/styk/internal/orderactions"
	"github.com/lilter96/styk/internal/orders"
)

// ReverseSuggestion derives a read-only proposal from the linked MoySklad order
// state. It never writes to Ozon: a ready proposal still has to pass the normal
// preview and approval journey, including operator-supplied event evidence.
func (s *Service) ReverseSuggestion(ctx context.Context, request orders.ReverseSuggestionRequest) (orders.ReverseSuggestion, error) {
	if request.Scope.Validate() != nil || request.PostingNumber == "" {
		return orders.ReverseSuggestion{}, orders.ErrInvalid
	}
	review, err := s.store.OrderReviewContext(ctx, request.Scope)
	if err != nil {
		return orders.ReverseSuggestion{}, err
	}
	selected, err := selectedPostings(review.Postings, []string{request.PostingNumber})
	if err != nil || len(selected) != 1 {
		return orders.ReverseSuggestion{}, orders.ErrNotFound
	}
	correlation, found := reverseCorrelation(review.Correlations, request.PostingNumber)
	if !found {
		return orders.ReverseSuggestion{}, orders.ErrNotFound
	}
	posting, err := s.readReversePosting(ctx, review, selected[0])
	if err != nil {
		return orders.ReverseSuggestion{}, err
	}
	target, err := s.readReverseTarget(ctx, review, correlation)
	if err != nil {
		return orders.ReverseSuggestion{}, err
	}
	return buildReverseSuggestion(review, posting, target, s.allowed(posting.Scope)), nil
}

func reverseCorrelation(correlations []orders.Correlation, postingNumber string) (orders.Correlation, bool) {
	for _, correlation := range correlations {
		if correlation.PostingNumber == postingNumber {
			return correlation, true
		}
	}
	return orders.Correlation{}, false
}

func (s *Service) readReversePosting(ctx context.Context, review orders.ReviewContext, stored orders.Posting) (orders.Posting, error) {
	client, err := resolveOrderClient(ctx, s.resolver, stored.Scope, "ozon", review.Credentials.Ozon)
	if err != nil {
		return orders.Posting{}, err
	}
	fresh, err := client.ReadOrderPosting(ctx, stored.Scope, stored.PostingNumber)
	if err != nil {
		return orders.Posting{}, fmt.Errorf("read current posting for reverse suggestion: %w", err)
	}
	if fresh.Scope != stored.Scope || fresh.PostingNumber != stored.PostingNumber || fresh.CommercialOrderID != stored.CommercialOrderID {
		return orders.Posting{}, orders.ErrStale
	}
	return fresh, nil
}

func (s *Service) readReverseTarget(ctx context.Context, review orders.ReviewContext, correlation orders.Correlation) (orders.TargetObservation, error) {
	client, err := resolveOrderClient(ctx, s.resolver, correlation.Scope, "moysklad", review.Credentials.Moysklad)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	reader, ok := client.(orders.TargetReader)
	if !ok {
		return orders.TargetObservation{}, orders.ErrBlocked
	}
	target, err := reader.ReadCustomerOrder(ctx, correlation)
	if err != nil {
		return orders.TargetObservation{}, fmt.Errorf("read linked MoySklad order for reverse suggestion: %w", err)
	}
	if !target.Complete || target.Scope != correlation.Scope || target.Correlation.PostingNumber != correlation.PostingNumber || target.Correlation.CustomerOrderID == "" {
		return orders.TargetObservation{}, orders.ErrStale
	}
	return target, nil
}

func buildReverseSuggestion(review orders.ReviewContext, posting orders.Posting, target orders.TargetObservation, workflowAllowed bool) orders.ReverseSuggestion {
	suggestion := orders.ReverseSuggestion{
		Scope: posting.Scope, PostingNumber: posting.PostingNumber, CommercialOrderID: posting.CommercialOrderID,
		CustomerOrderID: target.Correlation.CustomerOrderID, MoySkladStateID: target.Fields.StateID,
		PostingStatus: posting.Status, Substatus: posting.Substatus, SourceRevision: posting.Evidence.Revision,
		TargetRevision: target.Revision, ConfigRevision: review.Config.Revision,
		ParametersRequired: []string{}, Blockers: []orders.Validation{},
	}
	if !workflowAllowed || !review.Config.Enabled || !review.Config.TestEnabled {
		suggestion.Blockers = append(suggestion.Blockers, orders.Validation{Code: "order_workflow_disabled"})
	}
	mapping, mapped := reverseMapping(review.Config.ReverseStatusMappings, target.Fields.StateID)
	if !mapped {
		suggestion.Blockers = append(suggestion.Blockers, orders.Validation{Field: "moysklad_state", Code: "reverse_mapping_missing"})
		return suggestion
	}
	suggestion.ProposedAction = mapping.Action
	suggestion.ExpectedStatus = orders.ExpectedPostingStatus(mapping.Action)
	suggestion.ParametersRequired = orderactions.RequiredParameters(mapping.Action)
	if !posting.Status.IsKnown() {
		suggestion.Blockers = append(suggestion.Blockers, orders.Validation{Field: "status", Code: "reverse_source_status_unknown"})
	} else if suggestion.ExpectedStatus != "" && *posting.Status.Value == suggestion.ExpectedStatus {
		// The Ozon posting already reflects the target state. This is the echo
		// guard for status write-back loops.
		suggestion.Blockers = append(suggestion.Blockers, orders.Validation{Field: "status", Code: "reverse_action_already_applied"})
	} else if !orders.ReverseTransitionAllowed(mapping.Action, *posting.Status.Value) {
		suggestion.Blockers = append(suggestion.Blockers, orders.Validation{Field: "status", Code: "reverse_transition_not_available"})
	}
	suggestion.Ready = len(suggestion.Blockers) == 0
	return suggestion
}

func reverseMapping(mappings []orders.ReverseStatusMapping, stateID string) (orders.ReverseStatusMapping, bool) {
	if stateID == "" {
		return orders.ReverseStatusMapping{}, false
	}
	for _, mapping := range mappings {
		if mapping.StateID == stateID {
			return mapping, true
		}
	}
	return orders.ReverseStatusMapping{}, false
}
