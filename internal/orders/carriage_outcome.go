package orders

// OutcomeMatches confirms a carriage document only. This predicate never grants
// native demand, physical handoff, delivery or payment authority.
func (work CarriageWork) OutcomeMatches(actual CarriageSnapshot) bool {
	if work.DispatchCount != 1 || work.CarriageID == "" || actual.CarriageID != work.CarriageID || actual.Header == nil || actual.Header.CarriageID != work.CarriageID || actual.Header.CompanyID != work.Scope.OzonAccount || !work.Plan.MatchesMembers(actual) {
		return false
	}
	if work.Plan.Action == CarriageCreate {
		return actual.Header.Status == "new"
	}
	// The published schema does not enumerate approval states. Unknown states
	// remain unresolved; an HTTP acknowledgement is never substituted for GET.
	return false
}
