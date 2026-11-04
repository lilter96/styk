package orders

import "slices"

// SplitRetainedReservePermission is derived from a reconciled, root-only split
// and its immutable reviewed parent. It authorizes a reduction of that parent's
// existing owned reserve, never reservation of children or transfer completion.
// The store must independently reject earlier local split ancestry.
type SplitRetainedReservePermission struct {
	Scope       Scope       `json:"scope"`
	SplitWorkID string      `json:"split_work_id"`
	Before      Posting     `json:"before"`
	Correlation Correlation `json:"correlation"`
}

func (permission SplitRetainedReservePermission) Allows(posting Posting, baseline PositionBaseline) bool {
	if permission.Scope.Validate() != nil || permission.Scope != posting.Scope || permission.Before.Scope != permission.Scope || !validID(permission.SplitWorkID) {
		return false
	}
	if permission.Before.Validate() != nil || posting.Validate() != nil || permission.Correlation.Validate() != nil || permission.Correlation.Scope != permission.Scope || permission.Correlation.CustomerOrderID == "" {
		return false
	}
	if baseline.Validate() != nil || baseline.Scope != permission.Scope || baseline.Correlation != permission.Correlation || baseline.RecoveryID != "" || baseline.EvidenceClass == "OPERATOR_ATTESTED" {
		return false
	}
	if permission.Before.PostingNumber != posting.PostingNumber || permission.Correlation.PostingNumber != posting.PostingNumber || permission.Before.CommercialOrderID != posting.CommercialOrderID {
		return false
	}
	if permission.Before.ParentPostingNumber.IsKnown() || posting.ParentPostingNumber.IsKnown() || len(permission.Before.ChildPostingNumbers) > 0 {
		return false
	}
	return retainedSplitSourceReduced(permission.Before, posting, baseline)
}

func retainedSplitSourceReduced(before, posting Posting, baseline PositionBaseline) bool {
	original, invalid := reservationLineQuantities(before.Lines)
	if invalid.Code != "" || len(original) != len(posting.Lines) || len(original) != len(baseline.Bindings) {
		return false
	}
	current, invalid := reservationLineQuantities(posting.Lines)
	if invalid.Code != "" {
		return false
	}
	for _, binding := range baseline.Bindings {
		key := binding.Source.Identity.Key()
		previous, present := original[key]
		quantity, retained := current[key]
		if !present || !retained || !nonnegativeNotGreater(binding.Source.Quantity, previous) || !nonnegativeNotGreater(quantity, binding.Source.Quantity) {
			return false
		}
		if !binding.Source.Reserve.IsKnown() || !binding.Native.Reserve.IsKnown() || !binding.Native.Shipped.IsKnown() || !decimalZero(*binding.Native.Shipped.Value) {
			return false
		}
		delete(original, key)
	}
	return len(original) == 0
}

func ApplyRetainedSplitOpenQuantityReserve(posting Posting, write *CustomerOrderWrite, baseline PositionBaseline, permission SplitRetainedReservePermission) Validation {
	if write == nil || !permission.Allows(posting, baseline) {
		return reservePolicyValidation("posting.lineage", "reservation_policy_retained_split_proof_required")
	}
	candidate := *write
	candidate.Positions = slices.Clone(write.Positions)
	if invalid := applyOpenQuantityReserve(posting, &candidate, baseline, true); invalid.Code != "" {
		return invalid
	}
	if !retainedSplitWriteReduced(candidate, baseline) {
		return reservePolicyValidation("positions.reserve", "reservation_policy_retained_split_reduction_required")
	}
	write.Positions = candidate.Positions
	return Validation{}
}

func retainedSplitWriteReduced(write CustomerOrderWrite, baseline PositionBaseline) bool {
	if len(write.Positions) != len(baseline.Bindings) {
		return false
	}
	for _, wanted := range write.Positions {
		if !retainedSplitPositionReduced(wanted, baseline.Bindings) {
			return false
		}
	}
	return true
}

func retainedSplitPositionReduced(wanted PositionWrite, bindings []PositionBinding) bool {
	for _, binding := range bindings {
		if wanted.Identity.Key() != binding.Source.Identity.Key() {
			continue
		}
		return positionMappingsEqual(wanted.Mapping, binding.Source.Mapping) && nonnegativeNotGreater(wanted.Quantity, binding.Source.Quantity) && wanted.Reserve.IsKnown() && nonnegativeNotGreater(*wanted.Reserve.Value, *binding.Source.Reserve.Value) && desiredMoney(wanted.Price, binding.Source.Price) && desiredDecimal(wanted.VAT, binding.Source.VAT)
	}
	return false
}

// ValidateWrite checks a sealed intent without filling in absent reserve values.
// Final native preflight must still verify every UUID and current reserve.
func (permission SplitRetainedReservePermission) ValidateWrite(posting Posting, write CustomerOrderWrite, baseline PositionBaseline) error {
	candidate := write
	if invalid := ApplyRetainedSplitOpenQuantityReserve(posting, &candidate, baseline, permission); invalid.Code != "" {
		return ErrBlocked
	}
	for i, wanted := range write.Positions {
		if !SameObservedDecimal(wanted.Reserve, candidate.Positions[i].Reserve) {
			return ErrStale
		}
	}
	return nil
}
