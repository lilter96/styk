package orders

import "slices"

// FulfillmentStatusConfirmed recognizes only reviewed scheme-specific outcomes.
// A later observed state can confirm assembly, never physical stock movement,
// delivery authority, payment, or that this HTTP request caused the transition.
func FulfillmentStatusConfirmed(command Command, posting Posting) bool {
	if posting.Scope != command.Scope || posting.PostingNumber != command.PostingNumber || posting.CommercialOrderID != command.CommercialOrderID || !posting.Status.IsKnown() || posting.Status.Validate() != nil || knownFulfillmentFailure(posting) {
		return false
	}
	status := *posting.Status.Value
	expected := ExpectedPostingStatus(command.Action)
	if expected == "" {
		return false
	}
	if status == expected {
		return true
	}
	if !fulfillmentReadbackProfile(command, posting) {
		return false
	}
	capability := command.PostingAction.Capability
	switch command.Action {
	case ActionPostingPack:
		return packingReadbackContents(command, posting) && packingSuccessor(capability, posting)
	case ActionPostingShip:
		return sellerDeliveryReadback(capability, posting) && slices.Contains([]string{"driver_pickup", "delivered"}, status)
	case ActionPostingLastMile:
		return sellerDeliveryReadback(capability, posting) && status == "delivered"
	default:
		return false
	}
}

func knownFulfillmentFailure(posting Posting) bool {
	return posting.Substatus.IsKnown() && slices.Contains([]string{"ship_failed", "posting_registration_error"}, *posting.Substatus.Value)
}

func fulfillmentReadbackProfile(command Command, posting Posting) bool {
	if command.PostingAction == nil || DeliveryIntegrationConflict(posting) {
		return false
	}
	capability := command.PostingAction.Capability
	if capability.Validate() != nil || !posting.WarehouseID.IsKnown() || !posting.DeliveryMethodID.IsKnown() || capability.WarehouseID != *posting.WarehouseID.Value || capability.DeliveryMethodID != *posting.DeliveryMethodID.Value {
		return false
	}
	if posting.Scheme.IsKnown() && *posting.Scheme.Value != capability.Scheme {
		return false
	}
	for _, dimension := range []string{"scheme", "warehouse"} {
		if !slices.Contains(capability.VerifiedDimensions, dimension) {
			return false
		}
	}
	if posting.DeliveryProfile != nil {
		current := posting.DeliveryProfile
		if current.Validate() != nil || current.WarehouseID != capability.WarehouseID || current.DeliveryMethodID != capability.DeliveryMethodID || current.Scheme != capability.Scheme {
			return false
		}
		if current.Carrier.IsKnown() && capability.Carrier.IsKnown() && *current.Carrier.Value != *capability.Carrier.Value {
			return false
		}
		if current.Express.IsKnown() && capability.Express.IsKnown() && *current.Express.Value != *capability.Express.Value {
			return false
		}
	}
	return true
}

func packingReadbackContents(command Command, posting Posting) bool {
	packages := command.PostingAction.Parameters.Packages
	if len(packages) != 1 {
		return false
	}
	actual, ok := splitContents(posting.Lines)
	if !ok {
		return false
	}
	wanted, ok := splitPackages(packages)
	return ok && wanted[actual] == 1
}

func packingSuccessor(capability CapabilityRef, posting Posting) bool {
	status := *posting.Status.Value
	if capability.Scheme == "FBS" {
		return slices.Contains([]string{"acceptance_in_progress", "delivering", "driver_pickup", "delivered"}, status)
	}
	if capability.Scheme != "rFBS" || !standardReadbackDelivery(capability, posting) {
		return false
	}
	if sellerDeliveryReadback(capability, posting) {
		return slices.Contains([]string{"delivering", "driver_pickup", "delivered"}, status)
	}
	if !capability.Carrier.IsKnown() || *capability.Carrier.Value != "aggregator" || !slices.Contains(capability.VerifiedDimensions, "carrier") || !posting.Delivery.IntegrationType.IsKnown() || *posting.Delivery.IntegrationType.Value != "aggregator" {
		return false
	}
	return slices.Contains([]string{"awaiting_registration", "delivering", "driver_pickup", "delivered"}, status)
}

func standardReadbackDelivery(capability CapabilityRef, posting Posting) bool {
	return capability.Express.IsKnown() && !*capability.Express.Value && slices.Contains(capability.VerifiedDimensions, "express") && (!posting.Delivery.Express.IsKnown() || !*posting.Delivery.Express.Value)
}

func sellerDeliveryReadback(capability CapabilityRef, posting Posting) bool {
	return capability.Scheme == "rFBS" && standardReadbackDelivery(capability, posting) && capability.Carrier.IsKnown() && *capability.Carrier.Value == "non_integrated" && slices.Contains(capability.VerifiedDimensions, "carrier") && posting.Delivery.IntegrationType.IsKnown() && *posting.Delivery.IntegrationType.Value == "non_integrated"
}
