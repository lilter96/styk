package orders

import "strings"

func lineagePostingTrustworthy(posting Posting) bool {
	if posting.Validate() != nil || posting.Evidence.Origin != OriginOzon || !posting.Status.IsKnown() || posting.Substatus.IsKnown() && *posting.Substatus.Value == "ship_failed" {
		return false
	}
	if posting.Scheme.IsKnown() && strings.EqualFold(*posting.Scheme.Value, "FBO") {
		return false
	}
	for _, drift := range posting.Drift {
		if drift.Code != "schema_unverified" && drift.Code != "unknown_key" && drift.Code != "unknown_related_posting_field" {
			return false
		}
	}
	return true
}

func lineageStepValid(scope Scope, step SplitLineageStep) bool {
	work, before := step.Work, step.Before
	c, r := work.Command, work.Receipt
	if !lineagePostingTrustworthy(before) || before.Scope != scope || work.Scope != scope || !validID(work.ID) || work.State != StateReconciled || work.DispatchCount != 1 {
		return false
	}
	if c.Action != ActionPostingSplit && c.Action != ActionPostingSplitUnassembled || !validID(c.ID) || c.Scope != scope || c.PostingNumber != before.PostingNumber || c.CommercialOrderID != before.CommercialOrderID || c.ExpectedSourceRevision != before.Evidence.Revision || c.PostingAction == nil {
		return false
	}
	if c.Correlation.Validate() != nil || c.Correlation.Scope != scope || c.Correlation.PostingNumber != before.PostingNumber {
		return false
	}
	if r.State != StateReconciled || r.CommandID != c.ID || r.HTTPStatus < 200 || r.HTTPStatus >= 300 || len(r.PostingNumbers) > MaxSplitLineageHeads {
		return false
	}
	return lineageOutputsValid(before, c.PostingAction.Parameters.Packages, r.PostingNumbers, r.ObservedPostings)
}

// Unassembled response status transitions are not invented here. The normal
// provider adapter must have reconciled these actual detail allocations; this
// read proof validates contents, never grants the next provider action.
func lineageOutputsValid(before Posting, packages []ActionPackage, numbers []string, outputs []Posting) bool {
	if len(packages) < 2 || len(packages) > MaxSplitLineageHeads || len(numbers) != len(packages) || len(outputs) != len(numbers) {
		return false
	}
	wanted, ok := splitPackages(packages)
	if !ok || !splitConserved(before, packages) {
		return false
	}
	seen := make(map[string]bool, len(outputs))
	for _, posting := range outputs {
		if !lineagePostingTrustworthy(posting) || seen[posting.PostingNumber] || !lineageContainsNumber(numbers, posting.PostingNumber) || posting.Scope != before.Scope || posting.CommercialOrderID != before.CommercialOrderID {
			return false
		}
		if !lineageSameField(before.WarehouseID, posting.WarehouseID) || !lineageSameField(before.DeliveryMethodID, posting.DeliveryMethodID) || !splitTransferProductIdentities(before.Lines, posting.Lines) {
			return false
		}
		key, valid := splitContents(posting.Lines)
		if !valid || wanted[key] == 0 {
			return false
		}
		wanted[key]--
		seen[posting.PostingNumber] = true
	}
	return true
}

func lineageSameField(a, b Field[string]) bool {
	return a.Validate() == nil && b.Validate() == nil && a.IsKnown() && b.IsKnown() && *a.Value == *b.Value
}
func lineageContainsNumber(numbers []string, want string) bool {
	for _, number := range numbers {
		if number == want {
			return true
		}
	}
	return false
}
