package orderflow

import (
	"context"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/orderactions"
	"github.com/lilter96/styk/internal/orders"
)

func validateProposals(request orders.PreviewRequest) error {
	if len(request.Proposals) == 0 {
		return nil
	}
	if len(request.Proposals) != len(request.PostingNumbers) || len(request.Proposals) > orders.MaxPagePostings {
		return orders.ErrInvalid
	}
	// The operator console submits one proposal with its exact posting number
	// and repeats the action in the legacy actions list; nothing else may be
	// mixed into the same preview.
	for _, action := range request.Actions {
		if !slices.ContainsFunc(request.Proposals, func(proposal orders.ActionProposal) bool { return proposal.Action == action }) {
			return orders.ErrInvalid
		}
	}
	seen := make(map[string]bool, len(request.Proposals))
	for _, proposal := range request.Proposals {
		if !proposal.Action.Valid() || proposal.Action.IsMirror() || seen[proposal.PostingNumber] || !slices.Contains(request.PostingNumbers, proposal.PostingNumber) {
			return orders.ErrInvalid
		}
		seen[proposal.PostingNumber] = true
	}
	return nil
}

func (s *Service) planRequestedPosting(ctx context.Context, request orders.PreviewRequest, review orders.ReviewContext, posting orders.Posting, automatic bool, now time.Time) (orders.Decision, error) {
	for _, proposal := range request.Proposals {
		if proposal.PostingNumber == posting.PostingNumber {
			return s.planAction(ctx, review, posting, proposal, now)
		}
	}
	return s.planPosting(ctx, review, posting, request.Actions, automatic, now)
}

func (s *Service) planAction(ctx context.Context, review orders.ReviewContext, posting orders.Posting, proposal orders.ActionProposal, now time.Time) (orders.Decision, error) {
	posting.PII = nil
	decision := orders.Decision{Posting: posting, Commands: []orders.Command{}, Validation: sourceValidation(posting, review, now)}
	warehouse, validation := postingWarehouse(posting, review)
	if validation.Code != "" {
		decision.Validation = append(decision.Validation, validation)
	}
	reasons, err := s.actionReasons(ctx, review, posting, proposal.Action)
	if err != nil {
		decision.Validation = append(decision.Validation, orders.Validation{Field: "reason_id", Code: "provider_reasons_unavailable"})
	}
	decision.Validation = append(decision.Validation, orderactions.ValidateExecution(posting, warehouse.Capability, proposal, reasons, now)...)
	if len(decision.Validation) > 0 {
		return decision, nil
	}
	return s.freezeAction(ctx, review, decision, warehouse, proposal, now)
}

func (s *Service) freezeAction(ctx context.Context, review orders.ReviewContext, decision orders.Decision, warehouse orders.WarehouseMappingRef, proposal orders.ActionProposal, now time.Time) (orders.Decision, error) {
	correlation, err := postingCorrelation(decision.Posting, review.Correlations)
	if err != nil {
		return decision, err
	}
	public, ref, err := s.protectAction(ctx, review.Config, proposal, now)
	if err != nil {
		return decision, err
	}
	id, err := orderflowID()
	if err != nil {
		return decision, err
	}
	command := orders.Command{ID: id, Scope: review.Config.Scope, Action: proposal.Action, CommercialOrderID: decision.Posting.CommercialOrderID,
		PostingNumber: proposal.PostingNumber, Correlation: correlation, ConfigRevision: review.Config.Revision, Credentials: review.Credentials,
		ExpectedSourceRevision: decision.Posting.Evidence.Revision, SourceOrigin: orders.OriginOzon, Warehouse: warehouse, IdempotencyKey: id, CreatedAt: now,
		PostingAction: &orders.PostingAction{Capability: warehouse.Capability, Parameters: public, ProtectedInput: ref, ContractHash: orderactions.ExecutionContractHash()}}
	if err = command.Validate(); err != nil {
		return decision, err
	}
	decision.Correlation, decision.Commands, decision.Ready = correlation, []orders.Command{command}, true
	return decision, nil
}

func (s *Service) protectAction(ctx context.Context, config orders.Config, proposal orders.ActionProposal, now time.Time) (orders.ActionParameters, *orders.ProtectedActionInput, error) {
	if !proposal.Parameters.HasPrivateValues() {
		return proposal.Parameters.Public(), nil, nil
	}
	if s.options.ActionProtector == nil {
		return orders.ActionParameters{}, nil, orders.ErrBlocked
	}
	return s.options.ActionProtector.ProtectOrderActionInput(ctx, config, proposal.PostingNumber, proposal.Action, proposal.Parameters, now)
}

func (s *Service) actionReasons(ctx context.Context, review orders.ReviewContext, posting orders.Posting, action orders.Action) ([]orders.ActionReason, error) {
	if action != orders.ActionPostingCancel && action != orders.ActionPostingPartialCancel {
		return nil, nil
	}
	client, err := resolveOrderClient(ctx, s.resolver, posting.Scope, "ozon", review.Credentials.Ozon)
	if err != nil {
		return nil, err
	}
	reader, ok := client.(orders.ActionReasonReader)
	if !ok {
		return nil, orders.ErrBlocked
	}
	return reader.ReadOrderActionReasons(ctx, posting.Scope, posting.PostingNumber)
}

func (s *Service) ActionOptions(ctx context.Context, request orders.ActionOptionsRequest) (orders.ActionOptions, error) {
	review, err := s.store.OrderReviewContext(ctx, request.Scope)
	if err != nil {
		return orders.ActionOptions{}, err
	}
	selected, err := selectedPostings(review.Postings, []string{request.PostingNumber})
	if err != nil || len(selected) != 1 {
		return orders.ActionOptions{}, orders.ErrNotFound
	}
	return s.postingActionOptions(ctx, review, selected[0])
}

func (s *Service) postingActionOptions(ctx context.Context, review orders.ReviewContext, posting orders.Posting) (orders.ActionOptions, error) {
	warehouse, validation := postingWarehouse(posting, review)
	result := orders.ActionOptions{Scope: posting.Scope, PostingNumber: posting.PostingNumber, SourceRevision: posting.Evidence.Revision,
		Actions: make([]orders.ActionOption, 0, 10), CancellationReasons: []orders.ActionReason{}, LabelTypes: []string{"big_label", "small_label"}}
	reasons, reasonErr := s.actionReasons(ctx, review, posting, orders.ActionPostingCancel)
	if reasonErr == nil {
		result.CancellationReasons = reasons
	}
	for _, action := range orderactions.ExecutionActions() {
		plan := orderactions.PlanExecution(posting, warehouse.Capability, action, s.now())
		plan.Blockers = append(plan.Blockers, sourceValidation(posting, review, s.now())...)
		if validation.Code != "" {
			plan.Blockers = append(plan.Blockers, validation)
		}
		if !review.Config.Enabled || !review.Config.TestEnabled || !s.allowed(posting.Scope) {
			plan.Blockers = append(plan.Blockers, orders.Validation{Code: "order_workflow_disabled"})
		}
		if reasonErr != nil && (action == orders.ActionPostingCancel || action == orders.ActionPostingPartialCancel) {
			plan.Blockers = append(plan.Blockers, orders.Validation{Code: "provider_reasons_unavailable"})
		}
		plan.Allowed = len(plan.Blockers) == 0
		result.Actions = append(result.Actions, plan)
	}
	return result, nil
}
