package delivery

import (
	"github.com/lilter96/styk/internal/orders"
)

// Method integration is not the same as an obligation to execute a transition.
// The official method schema identifies who registers third-party orders; that
// alone does not establish who is allowed to report delivery for 3pl or hybrid.
func (p *Profile) classifyMethodIntegration(value orders.Field[string]) {
	if !value.IsMissing() {
		p.MethodIntegrationType = value
	}
	if !value.IsKnown() {
		return
	}
	p.VerifiedDimensions = append(p.VerifiedDimensions, "method_integration")
	switch *value.Value {
	case "non_integrated":
		p.CarrierIntegration = orders.Present("non_integrated")
	case "aggregator":
		p.CarrierIntegration = orders.Present("aggregator")
	case "3pl_tracking", "hybrid":
		return
	default:
		p.Drift = append(p.Drift, orders.Drift{Path: "$.delivery_methods[].tpl_integration_type", Code: "unknown_enum"})
		return
	}
	p.VerifiedDimensions = append(p.VerifiedDimensions, "carrier")
}

// IntegrationConflicts compares only exact method/posting facts from the same
// scope and identity. Missing facts never become non-integrated by default.
func (p Profile) IntegrationConflicts(posting orders.Posting) bool {
	if p.Scope != OrderScope(posting.Scope) || !p.MethodVerified || !p.MethodID.IsKnown() || !posting.DeliveryMethodID.IsKnown() || !posting.WarehouseID.IsKnown() {
		return false
	}
	if p.WarehouseID != *posting.WarehouseID.Value || *p.MethodID.Value != *posting.DeliveryMethodID.Value {
		return false
	}
	return p.MethodIntegrationType.IsKnown() && posting.Delivery.IntegrationType.IsKnown() && *p.MethodIntegrationType.Value != *posting.Delivery.IntegrationType.Value
}
