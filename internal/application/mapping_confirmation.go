package application

import (
	"context"
	"errors"

	"example.com/styk/internal/domain"
)

// MappingConfirmationStore owns validation and all writes in one transaction.
type MappingConfirmationStore interface {
	ConfirmMappingReview(context.Context, domain.MappingConfirmationRequest) (domain.MappingConfirmationResult, error)
}

type MappingConfirmation struct{ Store MappingConfirmationStore }

func (s MappingConfirmation) Confirm(ctx context.Context, req domain.MappingConfirmationRequest) (domain.MappingConfirmationResult, error) {
	if err := req.Validate(); err != nil {
		return domain.MappingConfirmationResult{}, err
	}
	if s.Store == nil {
		return domain.MappingConfirmationResult{}, errors.New("confirmation store required")
	}
	return s.Store.ConfirmMappingReview(ctx, req)
}
