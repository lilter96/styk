package orders

// SplitScalarMetadataAncestorTarget permits only the primary document of an
// explicitly reviewed metadata update to differ from its historical header.
// The complete current target is independently frozen by this command. It is
// not permission to update inventory or to refresh another ancestor's facts.
func SplitScalarMetadataAncestorTarget(c Command, index int, target TargetObservation) bool {
	p := c.SplitAncestorSettlement
	if !splitScalarMetadataCommand(c) || p == nil || p.Mode != SplitAncestorReduce || index < 0 || index >= len(p.Ancestors) || !c.PositionBaseline.Equal(p.Baseline) {
		return false
	}
	a := p.Ancestors[index]
	before := a.Target
	if a.Posting.PostingNumber != c.PostingNumber || before.Correlation != c.Correlation || target.Scope != c.Scope || target.Correlation != c.Correlation || !before.Complete || !target.Complete || !nativeDocumentPresent(before.SourceDeleted) || !nativeDocumentPresent(target.SourceDeleted) {
		return false
	}
	if !validID(target.Revision) || target.Revision != c.ExpectedTargetRevision || target.ObservedAt.IsZero() || !NativePositionsEqual(c.TargetPositions, target.Positions) || !NativePositionsEqual(before.Positions, target.Positions) {
		return false
	}
	return splitMetadataCriticalHeaders(before.Fields, target.Fields)
}

func splitMetadataCriticalHeaders(before, after CustomerOrderWrite) bool {
	if before.OrganizationID != after.OrganizationID || before.AgentID != after.AgentID || before.StoreID != after.StoreID || before.SalesChannelID != after.SalesChannelID || before.CurrencyID != after.CurrencyID {
		return false
	}
	return SameObservedDecimal(before.RateValue, after.RateValue) && sameNativeVATEnabled(before.Applicable, after.Applicable) && sameNativeVATEnabled(before.VATEnabled, after.VATEnabled) && sameNativeVATEnabled(before.VATIncluded, after.VATIncluded)
}
