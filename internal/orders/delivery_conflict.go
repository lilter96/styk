package orders

import "slices"

// DeliveryIntegrationConflict is an explicit contradiction, unlike absent
// metadata. Outgoing operations must not reuse authority from an older profile.
func DeliveryIntegrationConflict(posting Posting) bool {
	return slices.ContainsFunc(posting.Drift, func(d Drift) bool { return d.Code == "conflicting_integration" })
}
