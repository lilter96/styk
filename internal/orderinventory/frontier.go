package orderinventory

import (
	"github.com/lilter96/styk/internal/orderactions"
	"github.com/lilter96/styk/internal/orders"
)

func reservationFrontier(observations []Observation) (map[string]bool, bool) {
	excluded := make(map[string]bool, len(observations))
	byPosting := make(map[string]Observation, len(observations))
	for _, observation := range observations {
		byPosting[observationKey(observation.Posting)] = observation
	}
	valid := true
	for _, observation := range observations {
		posting := observation.Posting
		if posting.ParentPostingNumber.IsKnown() && posting.ParentPostingNumber.Value != nil {
			parent := posting
			parent.PostingNumber = *posting.ParentPostingNumber.Value
			_, found := byPosting[observationKey(parent)]
			valid = valid && found
		}
		if len(posting.ChildPostingNumbers) == 0 {
			continue
		}
		if !splitProven(observation, byPosting) {
			valid = false
			continue
		}
		excluded[observationKey(posting)] = true
	}
	return excluded, valid
}

func splitProven(parent Observation, byPosting map[string]Observation) bool {
	children := make([]orders.Posting, 0, len(parent.Posting.ChildPostingNumbers))
	if parent.Quarantined || parent.Truncated || len(parent.Posting.Drift) != 0 {
		return false
	}
	for _, number := range parent.Posting.ChildPostingNumbers {
		identity := parent.Posting
		identity.PostingNumber = number
		child, found := byPosting[observationKey(identity)]
		if !found || child.Quarantined || child.Truncated || len(child.Posting.Drift) != 0 {
			return false
		}
		children = append(children, child.Posting)
	}
	_, err := orderactions.FBSReservationFrontier(parent.Posting, children)
	return err == nil
}

func observationKey(posting orders.Posting) string {
	return posting.Scope.Tenant + "/" + posting.Scope.MoyskladAccount + "/" + posting.Scope.OzonAccount + "/" + posting.PostingNumber
}
