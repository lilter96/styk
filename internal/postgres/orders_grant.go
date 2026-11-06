package postgres

import (
	"slices"

	"time"

	"github.com/lilter96/styk/internal/orders"
)

// Permanent authority must be explicitly granted. A full config round trip may
// preserve or narrow current authority, but cannot expand it implicitly.
func validateOrderGrantChange(req orders.SaveConfigRequest, previous orders.Config, owner string, now time.Time) error {
	next := req.Config.Automation
	if next == nil || !next.Perpetual {
		return nil
	}
	prior := previous.Automation
	if req.AutomationConsent {
		return nil
	}
	if prior == nil || !prior.Perpetual || prior.OwnerID != owner || !prior.Active(now, previous.Scope, previous.Revision) || next.RevokedAt != nil {
		return orders.ErrBlocked
	}
	for _, action := range next.Actions {
		if !slices.Contains(prior.Actions, action) {
			return orders.ErrBlocked
		}
	}
	for _, field := range next.Fields {
		if !slices.Contains(prior.Fields, field) {
			return orders.ErrBlocked
		}
	}
	return nil
}

func orderGrantExpiry(grant orders.AutomationGrant) *time.Time {
	if grant.Perpetual {
		return nil
	}
	return &grant.ExpiresAt
}

func orderPostingStoreCurrent(posting orders.Posting, store string, mappings []orders.WarehouseMappingRef) bool {
	if !posting.WarehouseID.IsKnown() {
		return false
	}
	count := 0
	for _, mapping := range mappings {
		if mapping.Scope == posting.Scope && mapping.OzonWarehouseID == *posting.WarehouseID.Value && mapping.Validate() == nil {
			if mapping.MoyskladStoreID != store {
				return false
			}
			count++
		}
	}
	return count == 1
}
