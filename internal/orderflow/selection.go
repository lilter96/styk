package orderflow

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

// reviewSelector lets durable stores bound and rotate source selection before
// decoding postings. Small stores may keep the original review snapshot port.
type reviewSelector interface {
	OrderReviewContextFor(context.Context, orders.Scope, []string, bool) (orders.ReviewContext, error)
}

func (s *Service) reviewContextFor(ctx context.Context, request orders.PreviewRequest, automatic bool) (orders.ReviewContext, error) {
	if selector, ok := s.store.(reviewSelector); ok {
		return selector.OrderReviewContextFor(ctx, request.Scope, request.PostingNumbers, automatic)
	}
	return s.store.OrderReviewContext(ctx, request.Scope)
}
