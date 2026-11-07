package orderflow

import (
	"context"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

type CommissionPreviewRequest struct {
	Scope            orders.Scope
	ExpectedRevision int64
	Period           orders.RealizationPeriod
	Actor            string
}

type commissionReviewer interface {
	CommissionReviewContext(context.Context, orders.Scope) (orders.ReviewContext, error)
}

type commissionPreviews interface {
	SaveCommissionPreview(context.Context, orders.CommissionPreview) (orders.CommissionPreview, error)
	CommissionPreview(context.Context, orders.Scope, string) (orders.CommissionPreview, error)
}

type commissionApprover interface {
	ApproveCommissionPreview(context.Context, orders.CommissionApproval, time.Time) (orders.CommissionWork, error)
}

type commissionHistoryReader interface {
	CommissionHistory(context.Context, orders.Scope, int) ([]orders.CommissionWork, error)
}

func (s *Service) PreviewCommission(ctx context.Context, request CommissionPreviewRequest) (orders.CommissionPreview, error) {
	return s.previewCommission(ctx, request, false)
}

func (s *Service) previewCommission(ctx context.Context, request CommissionPreviewRequest, automatic bool) (orders.CommissionPreview, error) {
	if request.Scope.Validate() != nil || request.ExpectedRevision < 1 || strings.TrimSpace(request.Actor) == "" {
		return orders.CommissionPreview{}, orders.ErrInvalid
	}
	review, err := s.commissionReview(ctx, request.Scope, request.ExpectedRevision)
	if err != nil {
		return orders.CommissionPreview{}, err
	}
	report, err := s.RealizationReport(ctx, orders.RealizationRequest{Scope: request.Scope, Period: request.Period})
	if err != nil {
		return orders.CommissionPreview{}, err
	}
	references, referenceValidation := s.commissionPreviewReferences(ctx, review)
	id, err := orderflowID()
	if err != nil {
		return orders.CommissionPreview{}, err
	}
	plan, validation := ordermoney.PlanCommissionDraft(review.Config, review.Credentials, report, review.ProductMappings, references, id, s.now())
	validation = append(validation, referenceValidation...)
	preview := orders.CommissionPreview{ID: id, Plan: plan, Analysis: report.Analysis, Validation: validation, Actor: request.Actor, Automatic: automatic}
	if err = s.guardCommissionConfig(ctx, review.Config, review.Credentials); err != nil {
		return orders.CommissionPreview{}, err
	}
	store, ok := s.store.(commissionPreviews)
	if !ok {
		return orders.CommissionPreview{}, orders.ErrBlocked
	}
	return store.SaveCommissionPreview(ctx, preview)
}

func (s *Service) commissionReview(ctx context.Context, scope orders.Scope, revision int64) (orders.ReviewContext, error) {
	if !s.allowed(scope) {
		return orders.ReviewContext{}, orders.ErrBlocked
	}
	store, ok := s.store.(commissionReviewer)
	if !ok {
		return orders.ReviewContext{}, orders.ErrBlocked
	}
	review, err := store.CommissionReviewContext(ctx, scope)
	if err != nil {
		return review, err
	}
	if review.Config.Scope != scope || review.Config.Revision != revision {
		return review, orders.ErrStale
	}
	if !review.Config.ReadsEnabled() || review.Credentials.Ozon < 1 || review.Credentials.Moysklad < 1 {
		return review, orders.ErrBlocked
	}
	return review, nil
}

func (s *Service) commissionPreviewReferences(ctx context.Context, review orders.ReviewContext) (orders.CommissionReferences, []orders.Validation) {
	if review.Config.Finance == nil || !review.Config.Finance.Active(s.now(), review.Config.Scope, review.Config.Revision) {
		return orders.CommissionReferences{}, []orders.Validation{{Field: "finance", Code: "finance_policy_not_enabled"}}
	}
	references, err := s.commissionReferences(ctx, review)
	if err != nil {
		return references, []orders.Validation{{Field: "finance.references", Code: "finance_native_references_unavailable"}}
	}
	return references, nil
}

func (s *Service) ApproveCommission(ctx context.Context, approval orders.CommissionApproval) (orders.CommissionWork, error) {
	if approval.Automatic {
		return orders.CommissionWork{}, orders.ErrInvalid
	}
	return s.approveCommission(ctx, approval)
}

func (s *Service) approveCommission(ctx context.Context, approval orders.CommissionApproval) (orders.CommissionWork, error) {
	if approval.Scope.Validate() != nil || approval.PreviewID == "" || strings.TrimSpace(approval.Actor) == "" || approval.IdempotencyKey == "" {
		return orders.CommissionWork{}, orders.ErrInvalid
	}
	if !s.allowed(approval.Scope) {
		return orders.CommissionWork{}, orders.ErrBlocked
	}
	store, ok := s.store.(commissionApprover)
	if !ok {
		return orders.CommissionWork{}, orders.ErrBlocked
	}
	previews, ok := s.store.(commissionPreviews)
	if !ok {
		return orders.CommissionWork{}, orders.ErrBlocked
	}
	preview, err := previews.CommissionPreview(ctx, approval.Scope, approval.PreviewID)
	if err != nil {
		return orders.CommissionWork{}, err
	}
	if len(preview.Validation) != 0 || preview.Plan.Validate() != nil || preview.Plan.Scope != approval.Scope || preview.Actor != approval.Actor || preview.Automatic != approval.Automatic {
		return orders.CommissionWork{}, orders.ErrBlocked
	}
	return store.ApproveCommissionPreview(ctx, approval, s.now())
}

func (s *Service) CommissionOperations(ctx context.Context, scope orders.Scope, limit int) ([]orders.CommissionWork, error) {
	if scope.Validate() != nil || limit < 1 || limit > 100 {
		return nil, orders.ErrInvalid
	}
	store, ok := s.store.(commissionHistoryReader)
	if !ok {
		return nil, orders.ErrBlocked
	}
	return store.CommissionHistory(ctx, scope, limit)
}
