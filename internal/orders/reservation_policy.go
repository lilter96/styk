package orders

// ReserveOpenQuantitySource is an explicit, versioned mapping opt-in. It does
// not assert that a newly written reserve is already reflected in a stock
// baseline; commitment attribution still needs its own observation evidence.
const ReserveOpenQuantitySource = "posting.open_quantity"

// ApplyOpenQuantityReserve projects only known pending obligations, never a
// second inventory formula. Release proposals require existing STYK ownership;
// the native patch planner must still prove the current reserve is unchanged.
func ApplyOpenQuantityReserve(posting Posting, write *CustomerOrderWrite, baseline PositionBaseline) Validation {
	if validation := reservationPostingValidation(posting, write, baseline); validation.Code != "" {
		return validation
	}
	quantities := make(map[string]Decimal, len(posting.Lines))
	for _, line := range posting.Lines {
		if line.Validate() != nil || !positiveDecimal(line.Quantity) || quantities[line.Identity.Key()] != "" {
			return reservePolicyValidation("lines", "reservation_policy_invalid_line")
		}
		if line.CancelledQuantity.IsSpecified() && (!line.CancelledQuantity.IsKnown() || !decimalZero(*line.CancelledQuantity.Value)) {
			return reservePolicyValidation("lines.cancelled_quantity", "reservation_policy_partial_cancellation_unknown")
		}
		quantities[line.Identity.Key()] = line.Quantity
	}
	if len(write.Positions) != len(quantities) || len(quantities) == 0 {
		return reservePolicyValidation("positions.reserve", "reservation_policy_line_identity_mismatch")
	}
	for _, position := range write.Positions {
		quantity, ok := quantities[position.Identity.Key()]
		if !ok || !ExactDecimalEqual(quantity, position.Quantity) {
			return reservePolicyValidation("positions.reserve", "reservation_policy_line_identity_mismatch")
		}
		delete(quantities, position.Identity.Key())
	}
	for index := range write.Positions {
		reserve := write.Positions[index].Quantity
		if *posting.Status.Value == "cancelled" {
			reserve = "0"
		}
		write.Positions[index].Reserve = Present(reserve)
	}
	return Validation{}
}

func reservationPostingValidation(posting Posting, write *CustomerOrderWrite, baseline PositionBaseline) Validation {
	if write == nil || posting.Status.Validate() != nil || !posting.Status.IsKnown() {
		return reservePolicyValidation("posting.status", "reservation_policy_status_unknown")
	}
	if write.Applicable.Validate() != nil {
		return reservePolicyValidation("applicable", "reservation_policy_requires_applicable_true")
	}
	if optionalField(posting.Cancellation, func(value Cancellation) error { return value.Validate() }) != nil {
		return reservePolicyValidation("posting.cancellation", "reservation_policy_shipment_or_cancellation_conflict")
	}
	if len(posting.ChildPostingNumbers) > 0 || posting.ParentPostingNumber.IsKnown() {
		return reservePolicyValidation("posting.lineage", "reservation_policy_split_lineage_requires_review")
	}
	if posting.Times.DeliveringAt.IsKnown() || posting.Times.DeliveredAt.IsKnown() || reservationAfterShipment(posting.Cancellation) {
		return reservePolicyValidation("posting", "reservation_policy_shipment_or_cancellation_conflict")
	}
	switch *posting.Status.Value {
	case "cancelled":
		if baseline.Validate() != nil || baseline.Scope != posting.Scope || baseline.Correlation.PostingNumber != posting.PostingNumber {
			return reservePolicyValidation("positions.reserve", "reservation_policy_owned_release_required")
		}
	case "awaiting_registration", "awaiting_approve", "awaiting_packaging", "awaiting_deliver":
		if !write.Applicable.IsKnown() || !*write.Applicable.Value {
			return reservePolicyValidation("applicable", "reservation_policy_requires_applicable_true")
		}
		if posting.Times.DeliveringAt.IsKnown() || posting.Times.DeliveredAt.IsKnown() || reservationCancellationObserved(posting.Cancellation) {
			return reservePolicyValidation("posting", "reservation_policy_shipment_or_cancellation_conflict")
		}
	default:
		return reservePolicyValidation("posting.status", "reservation_policy_status_not_pending")
	}
	return Validation{}
}

func reservationCancellationObserved(value Field[Cancellation]) bool {
	if !value.IsKnown() {
		return !value.IsMissing() && value.Presence != PresenceNull
	}
	cancellation := value.Value
	return cancellation.AfterShipment.IsKnown() && *cancellation.AfterShipment.Value || cancellation.ReasonID.IsKnown() && *cancellation.ReasonID.Value != "0" || cancellation.Type.IsKnown() || cancellation.Initiator.IsKnown()
}

func reservePolicyValidation(field, code string) Validation {
	return Validation{Field: field, Code: code}
}

func reservationAfterShipment(value Field[Cancellation]) bool {
	return value.IsKnown() && value.Value.AfterShipment.IsKnown() && *value.Value.AfterShipment.Value
}
