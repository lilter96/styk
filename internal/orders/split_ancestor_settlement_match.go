package orders

func (a SplitAncestorNative) SourceMatches(p Posting) bool {
	proof := SplitReserveTransferPermission{ParentPosting: a.Posting}
	return proof.ParentSourceMatches(p)
}

func (a SplitAncestorNative) TargetMatches(t TargetObservation) bool {
	expected := a.Target
	return t.Scope == expected.Scope && t.Complete && t.Correlation == expected.Correlation && t.Revision == expected.Revision && nativeDocumentPresent(t.SourceDeleted) && t.Fields.StoreID == expected.Fields.StoreID && t.Fields.OrganizationID == expected.Fields.OrganizationID && t.Fields.Applicable.IsKnown() && expected.Fields.Applicable.IsKnown() && *t.Fields.Applicable.Value == *expected.Fields.Applicable.Value && NativePositionsEqual(t.Positions, expected.Positions)
}

func (p SplitAncestorSettlementPermission) AncestorNumbers() []string {
	result := make([]string, len(p.Ancestors))
	for i, a := range p.Ancestors {
		result[i] = a.Posting.PostingNumber
	}
	return result
}
