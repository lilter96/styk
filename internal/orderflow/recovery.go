package orderflow

import (
	"context"
	"errors"

	"github.com/lilter96/styk/internal/orders"
)

func (s *Service) Recovery(ctx context.Context, request orders.RecoveryRequest) (orders.RecoveryView, error) {
	store, ok := s.store.(orders.RecoveryStore)
	if !ok {
		return orders.RecoveryView{}, orders.ErrBlocked
	}
	current, err := store.OrderRecoveryContext(ctx, request)
	return s.allowedRecoveryView(current.View), err
}

func (s *Service) CheckRecovery(ctx context.Context, request orders.RecoveryRequest) (orders.RecoveryView, error) {
	store, ok := s.store.(orders.RecoveryStore)
	if !ok {
		return orders.RecoveryView{}, orders.ErrBlocked
	}
	current, err := store.OrderRecoveryContext(ctx, request)
	if err != nil {
		return orders.RecoveryView{}, err
	}
	if current.View.Work.State != orders.StateUnknown {
		return current.View, orders.ErrBlocked
	}
	evidence, err := s.readRecoveryEvidence(ctx, request, current)
	if err != nil {
		return orders.RecoveryView{}, err
	}
	view, err := store.SaveOrderRecoveryEvidence(ctx, evidence)
	return s.allowedRecoveryView(view), err
}

func (s *Service) readRecoveryEvidence(ctx context.Context, request orders.RecoveryRequest, current orders.RecoveryContext) (orders.RecoveryEvidence, error) {
	id, err := orderflowID()
	if err != nil {
		return orders.RecoveryEvidence{}, err
	}
	work := current.View.Work
	evidence := orders.RecoveryEvidence{ID: id, Scope: work.Scope, WorkID: work.ID, CommandFingerprint: work.CommandFingerprint, ConfigRevision: current.Config.Revision, Credentials: current.Credentials, StoredSourceRevision: current.Posting.Evidence.Revision, Actor: request.Actor, Code: "SOURCE_READ_UNAVAILABLE"}
	evidence.Source = s.readRecoverySource(ctx, current)
	if !work.Command.Action.IsMirror() {
		evidence.Code = "FULFILLMENT_UNKNOWN_READ_ONLY"
		if work.Command.PostingAction == nil {
			evidence.Code = "UNKNOWN_NATIVE_KIND_DISCOVERY_ONLY"
		}
		return evidence, nil
	}
	target, readErr := s.readRecoveryNative(ctx, current)
	if errors.Is(readErr, orders.ErrNotFound) {
		evidence.Code = "NATIVE_NOT_OBSERVED"
		return evidence, nil
	}
	if readErr != nil {
		evidence.Code = "NATIVE_READ_UNAVAILABLE"
		return evidence, nil
	}
	observed := orders.NewRecoveryTarget(target)
	evidence.Target = &observed
	evidence.Code = "NATIVE_STATE_OBSERVED"
	return evidence, nil
}

func (s *Service) readRecoverySource(ctx context.Context, current orders.RecoveryContext) *orders.RecoverySource {
	source, err := resolveOrderClient(ctx, s.resolver, current.View.Work.Scope, "ozon", current.Credentials.Ozon)
	if err != nil {
		return nil
	}
	posting, err := source.ReadOrderPosting(ctx, current.View.Work.Scope, current.View.Work.Command.PostingNumber)
	if err != nil || posting.Scope != current.View.Work.Scope || posting.PostingNumber != current.View.Work.Command.PostingNumber || posting.CommercialOrderID != current.View.Work.Command.CommercialOrderID {
		return nil
	}
	observed, err := orders.NewRecoverySource(posting)
	if err != nil {
		return nil
	}
	return &observed
}

func (s *Service) readRecoveryNative(ctx context.Context, current orders.RecoveryContext) (orders.TargetObservation, error) {
	work := current.View.Work
	client, err := resolveOrderClient(ctx, s.resolver, work.Scope, "moysklad", current.Credentials.Moysklad)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	correlation, err := client.ReadOrderCorrelation(ctx, work.Command.Correlation)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	reader, ok := client.(orders.TargetReader)
	if !ok {
		return orders.TargetObservation{}, orders.ErrBlocked
	}
	target, err := reader.ReadCustomerOrder(ctx, correlation)
	if err != nil {
		return orders.TargetObservation{}, err
	}
	if !validTarget(work.Command.Correlation, target) {
		return orders.TargetObservation{}, orders.ErrStale
	}
	return target, nil
}

func (s *Service) ResolveRecovery(ctx context.Context, request orders.RecoveryResolveRequest) (orders.RecoveryView, error) {
	if request.Validate() != nil {
		return orders.RecoveryView{}, orders.ErrInvalid
	}
	if !s.allowed(request.Scope) || s.options.RecoverySealer == nil {
		return orders.RecoveryView{}, orders.ErrBlocked
	}
	store, ok := s.store.(orders.RecoveryStore)
	if !ok {
		return orders.RecoveryView{}, orders.ErrBlocked
	}
	id, err := orderflowID()
	if err != nil {
		return orders.RecoveryView{}, err
	}
	request.AttestationID = id
	request.ReasonCiphertext, request.ReasonDigest, err = s.options.RecoverySealer.SealOrderRecoveryReason(request.Scope, request.WorkID, id, request.Reason)
	if err != nil {
		return orders.RecoveryView{}, err
	}
	return store.ResolveOrderRecovery(ctx, request)
}

func (s *Service) allowedRecoveryView(view orders.RecoveryView) orders.RecoveryView {
	if view.Eligible && !s.allowed(view.Work.Scope) {
		view.Eligible = false
		view.Blockers = append(view.Blockers, orders.Validation{Code: "ORDER_PUBLICATION_NOT_AUTHORIZED"})
	}
	return view
}
