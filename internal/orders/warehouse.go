package orders

// PostingWarehouse resolves exactly one confirmed link for this posting's
// account pair. Legacy Config.StoreID is deliberately absent from this decision.
func (review ReviewContext) PostingWarehouse(posting Posting) (WarehouseMappingRef, error) {
	if posting.Scope != review.Config.Scope || !posting.WarehouseID.IsKnown() {
		return WarehouseMappingRef{}, ErrBlocked
	}
	var result WarehouseMappingRef
	count := 0
	for _, mapping := range review.WarehouseMappings {
		if mapping.Scope != posting.Scope || mapping.OzonWarehouseID != *posting.WarehouseID.Value {
			continue
		}
		if mapping.Validate() != nil {
			return WarehouseMappingRef{}, ErrBlocked
		}
		result = mapping
		count++
	}
	if count != 1 {
		return WarehouseMappingRef{}, ErrBlocked
	}
	return result, nil
}
