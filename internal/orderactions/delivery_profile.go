package orderactions

import (
	"github.com/lilter96/styk/internal/orders"
	"slices"
)

// Known standard non-express follows the existing supported process. Express
// or unspecified profiles need a fresh, exact provider-advertised transition;
// absence of an optional actions list is not applicability evidence here.
func explicitDeliveryProfileAction(posting orders.Posting, cap orders.CapabilityRef, action orders.Action) orders.Validation {
	if cap.Express.IsKnown() && !*cap.Express.Value {
		return orders.Validation{}
	}
	token := ""
	switch action {
	case orders.ActionPostingShip:
		token = "non_int_delivering"
	case orders.ActionPostingLastMile:
		token = "non_int_last_mile"
	case orders.ActionPostingDeliver:
		token = "non_int_delivered"
	}
	if token == "" || !slices.Contains(posting.AvailableActions, token) {
		return actionBlock("available_actions", "explicit_delivery_action_required_for_profile")
	}
	return orders.Validation{}
}
