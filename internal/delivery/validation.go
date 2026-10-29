package delivery

import (
	"encoding/hex"
	"slices"

	"github.com/lilter96/styk/internal/orders"
)

func (scope Scope) Validate() error {
	if !safeID(scope.Tenant) || !safeID(scope.OzonAccount) {
		return orders.ErrInvalid
	}
	return nil
}

func (p Profile) Validate() error {
	if p.Scope.Validate() != nil || !safeID(p.WarehouseID) || len(p.ID) != 64 || p.Revision < 1 {
		return orders.ErrInvalid
	}
	if p.Contract.Validate() != nil || p.ObservedAt.IsZero() || p.CheckedAt.Before(p.ObservedAt) || !p.ValidUntil.After(p.CheckedAt) || p.ValidUntil.Sub(p.CheckedAt) > Validity {
		return orders.ErrInvalid
	}
	for _, field := range []orders.Field[string]{p.MethodID, p.Scheme, p.WarehouseType, p.WarehouseStatus, p.CarrierIntegration, p.DestinationType} {
		if field.Validate() != nil || (field.IsKnown() && !safeID(*field.Value)) {
			return orders.ErrInvalid
		}
	}
	if p.Express.Validate() != nil || (p.MethodVerified && !p.MethodID.IsKnown()) {
		return orders.ErrInvalid
	}
	seen := make(map[string]bool, len(p.VerifiedDimensions))
	for _, dimension := range p.VerifiedDimensions {
		if !slices.Contains([]string{"scheme", "warehouse", "warehouse_type", "warehouse_status", "delivery_method", "express", "carrier", "destination"}, dimension) || seen[dimension] {
			return orders.ErrInvalid
		}
		seen[dimension] = true
	}
	identity := p
	if identity.identify() != nil || identity.ID != p.ID || identity.Revision != p.Revision {
		return orders.ErrInvalid
	}
	return nil
}

func (e ContractEvidence) Validate() error {
	value, err := hex.DecodeString(e.Hash)
	if err != nil || len(value) != 32 || e.CheckedAt.IsZero() {
		return orders.ErrInvalid
	}
	return nil
}
