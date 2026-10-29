package application

import (
	"context"
	"errors"

	"example.com/styk/internal/domain"
)

type MappingReviewStore interface {
	CreateMappingReview(context.Context, domain.MappingReviewRequest) (domain.MappingReviewResult, error)
	InspectMappingReview(context.Context, string, string) (domain.MappingReviewResult, error)
}

// MappingReviews coordinates local review persistence without performing external calls.
type MappingReviews struct{ Store MappingReviewStore }

func (s MappingReviews) Create(ctx context.Context, r domain.MappingReviewRequest) (domain.MappingReviewResult, error) {
	if s.Store == nil {
		return domain.MappingReviewResult{}, errors.New("review store required")
	}
	return s.Store.CreateMappingReview(ctx, r)
}
func (s MappingReviews) Inspect(ctx context.Context, id, session string) (domain.MappingReviewResult, error) {
	if s.Store == nil {
		return domain.MappingReviewResult{}, errors.New("review store required")
	}
	return s.Store.InspectMappingReview(ctx, id, session)
}
