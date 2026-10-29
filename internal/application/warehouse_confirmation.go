package application

import (
	"context"
	"errors"

	"github.com/lilter96/styk/internal/domain"
)

type WarehouseConfirmationStore interface {
	ConfirmWarehouseReview(context.Context, domain.MappingConfirmationRequest) (domain.MappingConfirmationResult, error)
}
type WarehouseConfirmation struct{ Store WarehouseConfirmationStore }

func (s WarehouseConfirmation) Confirm(ctx context.Context, req domain.MappingConfirmationRequest) (domain.MappingConfirmationResult, error) {
	if err := req.Validate(); err != nil {
		return domain.MappingConfirmationResult{}, err
	}
	if s.Store == nil {
		return domain.MappingConfirmationResult{}, errors.New("warehouse confirmation store required")
	}
	return s.Store.ConfirmWarehouseReview(ctx, req)
}
