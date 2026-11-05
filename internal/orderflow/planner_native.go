package orderflow

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func validateNativeProposals(request orders.PreviewRequest) error {
	if len(request.NativeProposals) == 0 {
		return nil
	}
	if len(request.Proposals) != 0 || len(request.NativeProposals) != len(request.PostingNumbers) || len(request.NativeProposals) > orders.MaxPagePostings {
		return orders.ErrInvalid
	}
	seen := make(map[string]bool, len(request.NativeProposals))
	for _, proposal := range request.NativeProposals {
		if !proposal.Action.IsNativeMovement() || !slices.Contains(request.PostingNumbers, proposal.PostingNumber) || seen[proposal.PostingNumber] || !proposal.Applicable.IsKnown() || proposal.Applicable.Validate() != nil || len(proposal.Lines) == 0 || len(proposal.Lines) > 100 {
			return orders.ErrInvalid
		}
		if !*proposal.Applicable.Value && proposal.PlannedMoment.IsZero() {
			return orders.ErrInvalid
		}
		for _, action := range request.Actions {
			if action != proposal.Action {
				return orders.ErrInvalid
			}
		}
		seen[proposal.PostingNumber] = true
	}
	return nil
}

func (s *Service) planNativeMovement(ctx context.Context, review orders.ReviewContext, posting orders.Posting, proposal orders.NativeMovementProposal, automatic bool, now time.Time) (orders.Decision, error) {
	posting.PII = nil
	decision := orders.Decision{Posting: posting, Commands: []orders.Command{}, Validation: sourceValidation(posting, review, now)}
	if review.NativeInventoryActionBlocked(proposal.Action, posting.PostingNumber) {
		return nativePlanBlocked(decision, "native_documents", "native_split_fulfillment_allocation_unverified"), nil
	}
	if automatic || review.Config.NativeDocuments == nil || !proposal.Applicable.IsKnown() || !review.Config.NativeDocuments.Allows(proposal.Action, *proposal.Applicable.Value, now) {
		return nativePlanBlocked(decision, "native_documents", "native_document_policy_required"), nil
	}
	proposal, retryIssue := s.nativeRetryProposal(ctx, review, proposal)
	if retryIssue.Code != "" {
		return nativePlanBlocked(decision, retryIssue.Field, retryIssue.Code), nil
	}
	warehouse, validation := postingWarehouse(posting, review)
	if validation.Code != "" {
		decision.Validation = append(decision.Validation, validation)
	}
	correlation, err := postingCorrelation(posting, review.Correlations)
	if err != nil || correlation.CustomerOrderID == "" {
		return nativePlanBlocked(decision, "customer_order", "reconciled_native_order_required"), nil
	}
	baseline, found := review.PositionBaselines[posting.PostingNumber]
	if !found {
		return nativePlanBlocked(decision, "positions", "position_ownership_unverified"), nil
	}
	if len(decision.Validation) != 0 {
		return decision, nil
	}
	parent, err := s.readTarget(ctx, posting.Scope, review.Credentials.Moysklad, correlation)
	if err != nil {
		return nativePlanBlocked(decision, "customer_order", "fresh_native_order_unavailable"), nil
	}
	client, err := resolveOrderClient(ctx, s.resolver, posting.Scope, "moysklad", review.Credentials.Moysklad)
	if err != nil {
		return nativePlanBlocked(decision, "credentials", "native_credentials_unavailable"), nil
	}
	basis, err := s.nativeDemandBasis(ctx, &review, posting, proposal, client)
	if errors.Is(err, orders.ErrNativeDocumentDeleted) {
		return nativePlanBlocked(decision, "demand_basis", "native_document_deleted_requires_review"), nil
	}
	if err != nil {
		return nativePlanBlocked(decision, "demand_basis", "complete_reconciled_demand_basis_required"), nil
	}
	movement, validations := planNativeValues(review, posting, proposal, parent, baseline, basis)
	decision.Validation = append(decision.Validation, validations...)
	if len(decision.Validation) != 0 {
		return decision, nil
	}
	return s.freezeNativeMovement(ctx, review, decision, warehouse, parent, proposal, movement, client, now)
}

func nativePlanBlocked(decision orders.Decision, field, code string) orders.Decision {
	decision.Validation = append(decision.Validation, orders.Validation{Field: field, Code: code})
	return decision
}

func (s *Service) nativeDemandBasis(ctx context.Context, review *orders.ReviewContext, posting orders.Posting, proposal orders.NativeMovementProposal, client orders.Provider) (*orders.NativeDocumentObservation, error) {
	if proposal.Action == orders.ActionDemandCreate {
		return nil, nil
	}
	binding, found := review.NativeDocuments[proposal.DemandWorkID]
	if !found {
		lookup, ok := s.store.(orders.NativeDocumentLookup)
		if !ok {
			return nil, orders.ErrBlocked
		}
		var err error
		binding, err = lookup.OrderNativeDocument(ctx, posting.Scope, proposal.DemandWorkID)
		if err != nil {
			return nil, err
		}
		found = true
		if review.NativeDocuments == nil {
			review.NativeDocuments = make(map[string]orders.NativeDocumentBinding)
		}
		review.NativeDocuments[binding.WorkID] = binding
	}
	if !found || binding.WorkID != proposal.DemandWorkID || binding.Observation.Scope != posting.Scope || !binding.Observation.CustomerOrderID.IsKnown() {
		return nil, orders.ErrBlocked
	}
	reader, ok := client.(orders.NativeDocumentReader)
	if !ok {
		return nil, orders.ErrBlocked
	}
	current, err := reader.ReadNativeDocument(ctx, orders.NativeDocumentRequest{Scope: posting.Scope, Correlation: binding.Observation.Correlation, IncludeReturns: true})
	if current.SourceDeleted.IsKnown() && current.SourceDeleted.Value != nil && *current.SourceDeleted.Value != "" {
		return nil, orders.ErrNativeDocumentDeleted
	}
	if err != nil {
		return nil, err
	}
	return &current, nil
}

func (s *Service) freezeNativeMovement(ctx context.Context, review orders.ReviewContext, decision orders.Decision, warehouse orders.WarehouseMappingRef, parent orders.TargetObservation, proposal orders.NativeMovementProposal, movement orders.NativeMovement, client orders.Provider, now time.Time) (orders.Decision, error) {
	reader, ok := client.(orders.NativeProductReader)
	if !ok {
		return nativePlanBlocked(decision, "products", "native_product_requirements_unavailable"), nil
	}
	mappings := make([]orders.ProductMappingRef, 0, len(movement.Lines))
	for _, line := range movement.Lines {
		mappings = append(mappings, line.Mapping)
	}
	products, err := reader.ReadNativeProductRequirements(ctx, decision.Posting.Scope, mappings)
	if err != nil {
		return nativePlanBlocked(decision, "products", "bounded_native_product_requirements_unavailable"), nil
	}
	if validation := ordermoney.NativeDocumentAmounts(&movement); validation.Code != "" {
		return nativePlanBlocked(decision, validation.Field, validation.Code), nil
	}
	movement.Products = products
	return s.freezeNativeDecision(ctx, review, decision, warehouse, parent, proposal, movement, now)
}

func (s *Service) freezeNativeDecision(ctx context.Context, review orders.ReviewContext, decision orders.Decision, warehouse orders.WarehouseMappingRef, parent orders.TargetObservation, proposal orders.NativeMovementProposal, movement orders.NativeMovement, now time.Time) (orders.Decision, error) {
	protector, ok := s.options.ActionProtector.(orders.NativeEventProtector)
	if !ok {
		return nativePlanBlocked(decision, "event", "protected_native_event_required"), nil
	}
	event, err := protector.ProtectNativeMovementEvent(ctx, review.Config, proposal, now)
	movement.Event = event
	if err != nil {
		return nativePlanBlocked(decision, "event", "native_event_attestation_invalid"), nil
	}
	movement.Correlation, err = orders.NativeCorrelationFor(review.Config.Scope, proposal.Action, movement.Event.Digest)
	if err != nil {
		return decision, err
	}
	id, err := orderflowID()
	if err != nil {
		return decision, err
	}
	command := orders.Command{ID: id, Scope: review.Config.Scope, Action: proposal.Action, CommercialOrderID: decision.Posting.CommercialOrderID, PostingNumber: proposal.PostingNumber, Correlation: parent.Correlation, ConfigRevision: review.Config.Revision, Credentials: review.Credentials, ExpectedSourceRevision: decision.Posting.Evidence.Revision, ExpectedTargetRevision: parent.Revision, TargetPositions: slices.Clone(parent.Positions), TargetPositionsComplete: parent.Complete, SourceOrigin: orders.OriginOzon, Warehouse: warehouse, NativeMovement: &movement, IdempotencyKey: id, CreatedAt: now}
	if invalid := sealNativeConsumption(command, review, proposal); invalid.Code != "" {
		return nativePlanBlocked(decision, invalid.Field, invalid.Code), nil
	}
	if command.Validate() != nil || !movement.AllowedBy(review.Config.NativeDocuments, command.Action, now) {
		return nativePlanBlocked(decision, "native_document", "native_values_or_policy_unverified"), nil
	}
	safeParent := orders.NativeParentFacts(parent)
	decision.Target, decision.Correlation, decision.Commands, decision.Ready = &safeParent, parent.Correlation, []orders.Command{command}, true
	return decision, nil
}
