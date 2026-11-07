package orderflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type merchantIntentStore interface {
	PendingMerchantIntents(context.Context, orders.Scope, int) ([]orders.MerchantIntentStatus, error)
	BlockMerchantIntent(context.Context, orders.Scope, string, string) error
}

type merchantIntentQuerier interface {
	OrderMerchantIntents(context.Context, orders.Scope, int) ([]orders.MerchantIntentStatus, error)
}

func (s *Service) MerchantIntents(ctx context.Context, scope orders.Scope, limit int) ([]orders.MerchantIntentStatus, error) {
	if scope.Validate() != nil || limit < 1 || limit > 100 {
		return nil, orders.ErrInvalid
	}
	reader, ok := s.store.(merchantIntentQuerier)
	if !ok {
		return nil, orders.ErrBlocked
	}
	return reader.OrderMerchantIntents(ctx, scope, limit)
}

// ProcessMerchantIntents reuses preview, approval, outbox, guards and UNKNOWN
// readback. Multiple schedulers may review; only one DB CAS can bind an event.
func (s *Service) ProcessMerchantIntents(ctx context.Context, config orders.Config) error {
	policy := config.Lifecycle
	store, ok := s.store.(merchantIntentStore)
	if !ok || policy == nil || !config.Enabled || !config.TestEnabled || !s.allowed(config.Scope) || !policy.Active(s.now(), config.Scope, config.Revision) {
		return nil
	}
	items, err := store.PendingMerchantIntents(ctx, config.Scope, 10)
	if err != nil {
		return err
	}
	failures := make([]error, 0)
	for _, item := range items {
		if err = s.processMerchantIntent(ctx, config, item.Intent); err == nil {
			continue
		}
		if errors.Is(err, orders.ErrStale) || errors.Is(err, orders.ErrBlocked) || errors.Is(err, orders.ErrInvalid) || errors.Is(err, orders.ErrNotFound) {
			err = store.BlockMerchantIntent(ctx, config.Scope, item.Intent.ID, "MERCHANT_EVIDENCE_CHANGED_OR_INTERVENTION_REQUIRED")
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *Service) processMerchantIntent(ctx context.Context, config orders.Config, intent orders.MerchantIntent) error {
	if intent.Validate() != nil || intent.ConfigRevision != config.Revision || s.now().Before(intent.ObservedAt) || s.now().Sub(intent.ObservedAt) > 15*time.Minute {
		return orders.ErrStale
	}
	actor := "LIFECYCLE:" + config.Lifecycle.ID + ":" + strconv.FormatInt(config.Lifecycle.Revision, 10)
	request := orders.PreviewRequest{Scope: config.Scope, ExpectedRevision: config.Revision, Actor: actor, PostingNumbers: []string{intent.Correlation.PostingNumber}, Actions: []orders.Action{intent.Action}, MerchantIntent: &intent}
	review, err := s.reviewContextFor(ctx, request, true)
	if err != nil {
		return err
	}
	if err = validateReview(request, review); err != nil {
		return err
	}
	if len(review.Postings) != 1 || review.Config.Lifecycle == nil {
		return orders.ErrStale
	}
	if _, pending := review.ActivePostings[intent.Correlation.PostingNumber]; pending {
		return nil
	}
	if intent.Action.IsNativeMovement() {
		proposal, issue := AutomaticNativeProposal(review, review.Postings[0], intent)
		if issue.Code != "" {
			return s.blockMerchantIntent(ctx, intent, issue.Code)
		}
		request.NativeProposals = []orders.NativeMovementProposal{proposal}
	} else {
		proposal, issue := automaticMerchantAction(review, review.Postings[0], intent)
		if issue.Code != "" {
			return s.blockMerchantIntent(ctx, intent, issue.Code)
		}
		request.Proposals = []orders.ActionProposal{proposal}
	}
	return s.approveMerchantRequest(ctx, request, intent)
}

func (s *Service) blockMerchantIntent(ctx context.Context, intent orders.MerchantIntent, code string) error {
	store, ok := s.store.(merchantIntentStore)
	if !ok {
		return orders.ErrBlocked
	}
	return store.BlockMerchantIntent(ctx, intent.Scope, intent.ID, code)
}

func (s *Service) approveMerchantRequest(ctx context.Context, request orders.PreviewRequest, intent orders.MerchantIntent) error {
	preview, err := s.preview(ctx, request, true)
	if err != nil {
		return err
	}
	if len(readyPostingNumbers(preview)) == 0 {
		code := "MERCHANT_ACTION_REQUIRES_EVIDENCE"
		if len(preview.Decisions) > 0 && len(preview.Decisions[0].Validation) > 0 {
			code = preview.Decisions[0].Validation[0].Code
		}
		if slices.Contains([]string{"source_stale", "fresh_native_order_unavailable", "bounded_native_product_requirements_unavailable", "provider_reasons_unavailable", "target_readback_unavailable", "native_credentials_unavailable"}, code) {
			return s.waitMerchantIntent(ctx, intent, code)
		}
		return s.blockMerchantIntent(ctx, intent, code)
	}
	_, err = s.approve(ctx, orders.Approval{Scope: request.Scope, PreviewID: preview.ID, Actor: request.Actor, Automatic: true, IdempotencyKey: "merchant:" + intent.Digest})
	if err != nil {
		return fmt.Errorf("approve merchant intent through order outbox: %w", err)
	}
	return nil
}

type merchantWaitRecorder interface {
	WaitMerchantIntent(context.Context, orders.Scope, string, string) error
}

func (s *Service) waitMerchantIntent(ctx context.Context, intent orders.MerchantIntent, code string) error {
	recorder, ok := s.store.(merchantWaitRecorder)
	if !ok {
		return nil
	}
	return recorder.WaitMerchantIntent(ctx, intent.Scope, intent.ID, code)
}
