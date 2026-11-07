package orderflow

import (
	"context"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

type carriageReviewer interface {
	CarriageReviewContext(context.Context, orders.Scope, string) (orders.ReviewContext, error)
	SaveCarriagePreview(context.Context, orders.CarriagePreview) (orders.CarriagePreview, error)
}
type carriageApprover interface {
	ApproveCarriagePreview(context.Context, orders.CarriageApproval, time.Time) (orders.CarriageWork, error)
	CarriageHistory(context.Context, orders.Scope, int) ([]orders.CarriageWork, error)
	RequestCarriageReadback(context.Context, orders.Scope, string, string) error
}
type CarriagePreviewRequest struct {
	Scope            orders.Scope
	ExpectedRevision int64
	Actor            string
	Input            orders.CarriageInput
}

func (s *Service) PreviewCarriage(ctx context.Context, req CarriagePreviewRequest) (orders.CarriagePreview, error) {
	if req.Scope.Validate() != nil || req.ExpectedRevision < 1 || strings.TrimSpace(req.Actor) == "" || req.Input.Validate() != nil {
		return orders.CarriagePreview{}, orders.ErrInvalid
	}
	store, ok := s.store.(carriageReviewer)
	if !ok || !s.allowed(req.Scope) {
		return orders.CarriagePreview{}, orders.ErrBlocked
	}
	review, err := store.CarriageReviewContext(ctx, req.Scope, req.Actor)
	if err != nil {
		return orders.CarriagePreview{}, err
	}
	if review.Config.Revision != req.ExpectedRevision {
		return orders.CarriagePreview{}, orders.ErrStale
	}
	binding, err := s.resolver.ResolveOrder(ctx, req.Scope, "ozon", req.Scope.OzonAccount, review.Credentials.Ozon)
	if err != nil {
		return orders.CarriagePreview{}, err
	}
	reader, err := carriageReaderBinding(binding, req.Scope)
	if err != nil {
		return orders.CarriagePreview{}, err
	}
	guard := func(check context.Context) error { return s.guardCarriageReview(check, req, review.Credentials) }
	if err = guard(ctx); err != nil {
		return orders.CarriagePreview{}, err
	}
	snapshot, err := reader.ReadCarriageBatch(providercall.WithGuard(ctx, guard), req.Scope, req.Input)
	if err != nil {
		return orders.CarriagePreview{}, err
	}
	if err = guard(ctx); err != nil {
		return orders.CarriagePreview{}, err
	}
	preview, err := s.freezeCarriagePreview(req, review, snapshot)
	if err != nil {
		return orders.CarriagePreview{}, err
	}
	return store.SaveCarriagePreview(ctx, preview)
}

func (s *Service) freezeCarriagePreview(req CarriagePreviewRequest, review orders.ReviewContext, snapshot orders.CarriageSnapshot) (orders.CarriagePreview, error) {
	id, err := orderflowID()
	if err != nil {
		return orders.CarriagePreview{}, err
	}
	now := s.now()
	plan := orders.CarriagePlan{ID: id, Scope: req.Scope, CarriageInput: req.Input, WarehouseID: snapshot.WarehouseID, Members: snapshot.Members, Snapshot: snapshot, ConfigRevision: review.Config.Revision, Credentials: review.Credentials, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	plan.MembershipFingerprint, err = orders.CarriageMembershipFingerprint(plan.Members)
	if err != nil {
		return orders.CarriagePreview{}, err
	}
	plan.Fingerprint, err = orders.CarriagePlanFingerprint(plan)
	if err != nil {
		return orders.CarriagePreview{}, err
	}
	return orders.CarriagePreview{ID: id, Plan: plan, Validation: plan.ReadinessIssues(now), Actor: req.Actor}, nil
}

func carriageReaderBinding(binding orders.Binding, scope orders.Scope) (orders.CarriageReader, error) {
	if binding.Scope != scope || binding.Provider != "ozon" || binding.Account != scope.OzonAccount || binding.CredentialRevision < 1 {
		return nil, orders.ErrStale
	}
	reader, ok := binding.Client.(orders.CarriageReader)
	if !ok {
		return nil, orders.ErrBlocked
	}
	return reader, nil
}

func (s *Service) guardCarriageReview(ctx context.Context, req CarriagePreviewRequest, credentials orders.CredentialRevisions) error {
	store, ok := s.store.(carriageReviewer)
	if !ok || !s.allowed(req.Scope) {
		return orders.ErrBlocked
	}
	review, err := store.CarriageReviewContext(ctx, req.Scope, req.Actor)
	if err != nil {
		return err
	}
	if review.Config.Revision != req.ExpectedRevision || review.Credentials != credentials {
		return orders.ErrStale
	}
	return nil
}

func (s *Service) ApproveCarriage(ctx context.Context, approval orders.CarriageApproval) (orders.CarriageWork, error) {
	store, ok := s.store.(carriageApprover)
	if !ok || !s.allowed(approval.Scope) {
		return orders.CarriageWork{}, orders.ErrBlocked
	}
	if !approval.AllReadyConsent || !approval.PhysicalReadinessConfirmed || approval.Actor == "" {
		return orders.CarriageWork{}, orders.ErrInvalid
	}
	return store.ApproveCarriagePreview(ctx, approval, s.now())
}

func (s *Service) CarriageOperations(ctx context.Context, scope orders.Scope, limit int) ([]orders.CarriageWork, error) {
	store, ok := s.store.(carriageApprover)
	if !ok || !s.allowed(scope) {
		return nil, orders.ErrBlocked
	}
	return store.CarriageHistory(ctx, scope, limit)
}

func (s *Service) RereadCarriage(ctx context.Context, scope orders.Scope, workID, actor string) error {
	store, ok := s.store.(carriageApprover)
	if !ok || !s.allowed(scope) {
		return orders.ErrBlocked
	}
	return store.RequestCarriageReadback(ctx, scope, workID, actor)
}
