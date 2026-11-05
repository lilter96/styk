package orders

// A split changes allocation identities. Gross physical fulfillment must not
// become a fresh child shipment allowance without an explicit lineage proof.
func (review ReviewContext) NativeInventoryActionBlocked(action Action, posting string) bool {
	return review.NativeSplitBlocked[posting] && action.IsSplit() ||
		review.NativeAncestorConsumed[posting] && (action == ActionDemandCreate || action.IsMirror())
}
