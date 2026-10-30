package orderflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type ServiceOptions struct {
	AllowScope      func(orders.Scope) bool
	Now             func() time.Time
	CustomerReader  orders.CustomerReader
	ActionProtector orders.ActionInputProtector
}

// Service freezes scoped facts and commands for review. The store repeats all
// authority, revision and mapping checks when a preview is saved and approved.
type Service struct {
	store    orders.ServiceStore
	resolver orders.Resolver
	options  ServiceOptions
}

func NewService(store orders.ServiceStore, resolver orders.Resolver, options ServiceOptions) (*Service, error) {
	if store == nil || resolver == nil {
		return nil, orders.ErrInvalid
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: store, resolver: resolver, options: options}, nil
}

func (s *Service) Config(ctx context.Context, scope orders.Scope) (orders.Config, error) {
	return s.store.OrderConfig(ctx, scope)
}

func (s *Service) SaveConfig(ctx context.Context, request orders.SaveConfigRequest) (orders.Config, error) {
	if request.Config.Scope.Validate() != nil || strings.TrimSpace(request.Actor) == "" {
		return orders.Config{}, orders.ErrInvalid
	}
	if (request.Config.Enabled || request.Config.TestEnabled) && !s.allowed(request.Config.Scope) {
		return orders.Config{}, orders.ErrBlocked
	}
	return s.store.SaveOrderConfig(ctx, request, s.now())
}

func (s *Service) Postings(ctx context.Context, scope orders.Scope, request orders.PageRequest) (orders.PostingPage, error) {
	return s.store.OrderPostings(ctx, scope, request)
}

func (s *Service) History(ctx context.Context, scope orders.Scope, request orders.PageRequest) (orders.HistoryPage, error) {
	return s.store.OrderHistory(ctx, scope, request)
}

func (s *Service) Preview(ctx context.Context, request orders.PreviewRequest) (orders.Preview, error) {
	return s.preview(ctx, request, false)
}

func (s *Service) preview(ctx context.Context, request orders.PreviewRequest, automatic bool) (orders.Preview, error) {
	if err := s.validatePreviewRequest(request); err != nil {
		return orders.Preview{}, err
	}
	review, err := s.reviewContextFor(ctx, request, automatic)
	if err != nil {
		return orders.Preview{}, fmt.Errorf("read order review facts: %w", err)
	}
	if err = validateReview(request, review); err != nil {
		return orders.Preview{}, err
	}
	postings, err := selectedPostings(review.Postings, request.PostingNumbers)
	if err != nil {
		return orders.Preview{}, err
	}
	preview, err := s.planPreview(ctx, request, review, postings, automatic)
	if err != nil {
		return orders.Preview{}, err
	}
	return s.store.SaveOrderPreview(ctx, preview)
}

func (s *Service) validatePreviewRequest(request orders.PreviewRequest) error {
	if request.Scope.Validate() != nil || request.ExpectedRevision < 1 || strings.TrimSpace(request.Actor) == "" || len(request.PostingNumbers) > orders.MaxPagePostings {
		return orders.ErrInvalid
	}
	if !s.allowed(request.Scope) {
		return orders.ErrBlocked
	}
	if err := validateProposals(request); err != nil {
		return err
	}
	seen := make(map[orders.Action]bool, len(request.Actions))
	for _, action := range request.Actions {
		if !action.Valid() || seen[action] {
			return orders.ErrInvalid
		}
		seen[action] = true
	}
	return nil
}

func validateReview(request orders.PreviewRequest, review orders.ReviewContext) error {
	if review.Config.Scope != request.Scope || review.Config.Revision != request.ExpectedRevision {
		return orders.ErrStale
	}
	if !review.Config.Enabled || !review.Config.TestEnabled {
		return orders.ErrBlocked
	}
	if review.Config.Validate() != nil || review.Credentials.Ozon < 1 || review.Credentials.Moysklad < 1 {
		return orders.ErrInvalid
	}
	return nil
}

func (s *Service) Approve(ctx context.Context, approval orders.Approval) (orders.Operation, error) {
	if approval.Scope.Validate() != nil || approval.PreviewID == "" || strings.TrimSpace(approval.Actor) == "" || approval.Automatic {
		return orders.Operation{}, orders.ErrInvalid
	}
	if !s.allowed(approval.Scope) {
		return orders.Operation{}, orders.ErrBlocked
	}
	return s.approve(ctx, approval)
}

func (s *Service) approve(ctx context.Context, approval orders.Approval) (orders.Operation, error) {
	preview, err := s.store.OrderPreview(ctx, approval.Scope, approval.PreviewID)
	if err != nil {
		return orders.Operation{}, fmt.Errorf("read reviewed order preview: %w", err)
	}
	if len(approval.PostingNumbers) == 0 {
		approval.PostingNumbers = readyPostingNumbers(preview)
	}
	if len(approval.PostingNumbers) == 0 {
		return orders.Operation{}, orders.ErrBlocked
	}
	return s.store.ApproveOrderPreview(ctx, approval, s.now())
}

func readyPostingNumbers(preview orders.Preview) []string {
	result := make([]string, 0, len(preview.Decisions))
	for _, decision := range preview.Decisions {
		if decision.Ready && len(decision.Validation) == 0 && len(decision.Commands) == 1 {
			result = append(result, decision.Posting.PostingNumber)
		}
	}
	return result
}

func (s *Service) allowed(scope orders.Scope) bool {
	return s.options.AllowScope != nil && s.options.AllowScope(scope)
}

func (s *Service) now() time.Time { return s.options.Now().UTC() }
