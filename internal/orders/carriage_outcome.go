package orders

import (
	"slices"
)

// OutcomeMatches confirms a carriage document only. This predicate never grants
// native demand, physical handoff, delivery or payment authority.
func (work CarriageWork) OutcomeMatches(actual CarriageSnapshot) bool {
	if work.DispatchCount != 1 || work.CarriageID == "" || actual.CarriageID != work.CarriageID || actual.Header == nil || actual.Header.CarriageID != work.CarriageID || actual.Header.CompanyID != work.Scope.OzonAccount || actual.Header.DeliveryMethodID != work.Plan.DeliveryMethodID || actual.Header.WarehouseID != work.Plan.WarehouseID || !work.Plan.MatchesMembers(actual) {
		return false
	}
	if work.Plan.Action == CarriageCreate {
		return actual.Header.Status == "new"
	}
	if work.Plan.Action != CarriageApprove || work.Plan.Snapshot.Header == nil || work.Plan.Snapshot.Header.Status != "new" || actual.Header.Status == "new" || actual.Header.Status == "cancelled" || !actual.Header.UpdatedAt.After(work.Plan.Snapshot.Header.UpdatedAt) {
		return false
	}
	if work.Plan.ContainersCount != nil && (actual.Header.ContainersCount == nil || *actual.Header.ContainersCount != *work.Plan.ContainersCount) {
		return false
	}
	// Confirmation uses independent document readiness and exact composition,
	// rather than inventing an enum for the undocumented carriage state string.
	documents := actual.Documents
	if documents == nil || documents.Status != "ready" || documents.Partial || len(documents.Removed) != 0 || len(documents.Added) != len(work.Plan.Members) {
		return false
	}
	for _, member := range work.Plan.Members {
		if !slices.Contains(documents.Added, member.PostingNumber) {
			return false
		}
	}
	for _, action := range actual.Header.AvailableActions {
		if slices.Contains([]string{"get_shipping_list", "get_act_of_acceptance", "get_waybill"}, action) {
			return true
		}
	}
	return false
}

func sameCarriageComposition(before, after []CarriageMember) bool {
	if len(before) != len(after) {
		return false
	}
	project := func(members []CarriageMember) []string {
		values := make([]string, 0)
		for _, m := range members {
			prefix := m.PostingNumber + "\x00" + m.Scheme + "\x00" + m.WarehouseID + "\x00" + m.DeliveryMethodID
			values = append(values, prefix)
			for _, line := range m.Lines {
				values = append(values, prefix+"\x00"+line.Identity.SKU+"\x00"+line.Identity.OfferID+"\x00"+string(line.Quantity))
			}
		}
		slices.Sort(values)
		return values
	}
	return slices.Equal(project(before), project(after))
}
