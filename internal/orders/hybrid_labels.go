package orders

import (
	"context"
	"time"
)

// HybridLabelRequired follows official Ozon notice 638: seller-delivered hybrid
// postings need a printed label before the seller reports handing them over.
func HybridLabelRequired(posting Posting, action Action) bool {
	return action == ActionPostingShip && posting.Delivery.IntegrationType.IsKnown() && *posting.Delivery.IntegrationType.Value == "hybrid"
}

// LabelProof is non-PII evidence that one exact posting's provider label task
// finished and its PDF was stored. Actual printing remains operator-attested.
type LabelProof struct {
	Scope                                                                   Scope
	WorkID, PostingNumber, CommercialOrderID, WarehouseID, DeliveryMethodID string
	TaskID                                                                  string
	ExpiresAt                                                               time.Time
}

func (proof LabelProof) ValidateFor(posting Posting, reference string, now time.Time) error {
	if posting.Evidence.Origin != OriginOzon || proof.Scope != posting.Scope || !validID(reference) || proof.WorkID != reference || !validID(proof.TaskID) || !proof.ExpiresAt.After(now) {
		return ErrStale
	}
	if proof.PostingNumber != posting.PostingNumber || proof.CommercialOrderID != posting.CommercialOrderID {
		return ErrStale
	}
	if !posting.WarehouseID.IsKnown() || !posting.DeliveryMethodID.IsKnown() || proof.WarehouseID != *posting.WarehouseID.Value || proof.DeliveryMethodID != *posting.DeliveryMethodID.Value {
		return ErrStale
	}
	return nil
}

type LabelProofReader interface {
	OrderLabelProof(context.Context, Scope, string, string) (LabelProof, error)
}
