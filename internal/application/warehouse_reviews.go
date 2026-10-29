package application

import (
	"context"
	"errors"
	"github.com/lilter96/styk/internal/domain"
)

type WarehouseReviewStore interface {
	CreateWarehouseReview(context.Context, domain.WarehouseReviewRequest) (domain.WarehouseReviewResult, error)
	InspectWarehouseReview(context.Context, string, string) (domain.WarehouseReviewResult, error)
}
type WarehouseReviews struct{ Store WarehouseReviewStore }

func (s WarehouseReviews) Create(ctx context.Context, r domain.WarehouseReviewRequest) (domain.WarehouseReviewResult, error) {
	if s.Store == nil {
		return domain.WarehouseReviewResult{}, errors.New("warehouse review store required")
	}
	return s.Store.CreateWarehouseReview(ctx, r)
}
func (s WarehouseReviews) Inspect(ctx context.Context, id, session string) (domain.WarehouseReviewResult, error) {
	if s.Store == nil {
		return domain.WarehouseReviewResult{}, errors.New("warehouse review store required")
	}
	return s.Store.InspectWarehouseReview(ctx, id, session)
}
