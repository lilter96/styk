package orders

import (
	"encoding/json"
	"slices"
	"strings"
)

func supersededPermissionValues(facts SplitSupersededReserveFacts) SplitSupersededReservePermission {
	return SplitSupersededReservePermission{Scope: facts.Before.Scope, SplitWorkID: facts.Split.ID, Before: facts.Before, ParentPosting: facts.Parent, ChildPostings: facts.Children, ParentCorrelation: facts.OriginalCorrelation, Baseline: facts.Baseline, ParentTargetRevision: facts.Target.Revision, ParentPositions: facts.Target.Positions, ParentStoreID: facts.Target.Fields.StoreID, ParentOrganizationID: facts.Target.Fields.OrganizationID}
}

func freezeSupersededFacts(facts SplitSupersededReserveFacts) (SplitSupersededReserveFacts, error) {
	facts.Before, facts.Parent = minimalSupersededPosting(facts.Before), minimalSupersededPosting(facts.Parent)
	facts.Children = minimalSupersededPostings(facts.Children)
	facts.Split = minimalSupersededWork(facts.Split)
	target := facts.Target
	facts.Target = TargetObservation{Scope: target.Scope, Correlation: target.Correlation, Revision: target.Revision, Complete: target.Complete, ObservedAt: target.ObservedAt, SourceDeleted: target.SourceDeleted, Positions: target.Positions, Fields: CustomerOrderWrite{OrganizationID: target.Fields.OrganizationID, StoreID: target.Fields.StoreID, Applicable: target.Fields.Applicable}}
	raw, err := json.Marshal(facts)
	if err != nil {
		return SplitSupersededReserveFacts{}, ErrInvalid
	}
	var result SplitSupersededReserveFacts
	if json.Unmarshal(raw, &result) != nil {
		return SplitSupersededReserveFacts{}, ErrInvalid
	}
	canonicalSupersededPosting(&result.Before)
	canonicalSupersededPosting(&result.Parent)
	for i := range result.Children {
		canonicalSupersededPosting(&result.Children[i])
	}
	for i := range result.Split.Receipt.ObservedPostings {
		canonicalSupersededPosting(&result.Split.Receipt.ObservedPostings[i])
	}
	slices.SortFunc(result.Children, func(a, b Posting) int { return strings.Compare(a.PostingNumber, b.PostingNumber) })
	return result, nil
}

func minimalSupersededWork(work Work) Work {
	c, r := work.Command, work.Receipt
	command := Command{ID: c.ID, Scope: c.Scope, Action: c.Action, PostingNumber: c.PostingNumber, CommercialOrderID: c.CommercialOrderID, ExpectedSourceRevision: c.ExpectedSourceRevision, Correlation: c.Correlation, Warehouse: c.Warehouse}
	if c.PostingAction != nil {
		command.PostingAction = &PostingAction{Parameters: ActionParameters{Packages: c.PostingAction.Parameters.Packages}}
	}
	return Work{ID: work.ID, Scope: work.Scope, State: work.State, DispatchCount: work.DispatchCount, Command: command, Receipt: Receipt{CommandID: r.CommandID, State: r.State, HTTPStatus: r.HTTPStatus, PostingNumbers: r.PostingNumbers, ObservedPostings: minimalSupersededPostings(r.ObservedPostings)}}
}

func minimalSupersededPostings(postings []Posting) []Posting {
	result := make([]Posting, len(postings))
	for i, p := range postings {
		result[i] = minimalSupersededPosting(p)
	}
	return result
}

func minimalSupersededPosting(p Posting) Posting {
	lines := slices.Clone(p.Lines)
	for i := range lines {
		lines[i].Name = Field[string]{}
	}
	return Posting{Scope: p.Scope, CommercialOrderID: p.CommercialOrderID, PostingNumber: p.PostingNumber, ParentPostingNumber: p.ParentPostingNumber, ChildPostingNumbers: p.ChildPostingNumbers, RelatedPostings: p.RelatedPostings, Status: p.Status, Substatus: p.Substatus, WarehouseID: p.WarehouseID, DeliveryMethodID: p.DeliveryMethodID, Scheme: p.Scheme, Times: p.Times, Cancellation: p.Cancellation, Lines: lines, Evidence: p.Evidence, Drift: p.Drift}
}

func canonicalSupersededPosting(posting *Posting) {
	posting.RelatedPostings = posting.RelatedPostings.Canonical()
	slices.SortFunc(posting.Lines, func(a, b PostingLine) int { return strings.Compare(a.Identity.Key(), b.Identity.Key()) })
	slices.Sort(posting.ChildPostingNumbers)
}

func supersededSourcesTrustworthy(facts SplitSupersededReserveFacts) bool {
	for _, p := range append([]Posting{facts.Before, facts.Parent}, append(slices.Clone(facts.Children), facts.Split.Receipt.ObservedPostings...)...) {
		if p.Validate() != nil || p.Substatus.IsKnown() && *p.Substatus.Value == "ship_failed" {
			return false
		}
		for _, drift := range p.Drift {
			if drift.Code != "schema_unverified" && drift.Code != "unknown_key" && drift.Code != "unknown_related_posting_field" {
				return false
			}
		}
	}
	return true
}

func supersededNativeFrontierValid(positions []TargetPosition) bool {
	if len(positions) == 0 || len(positions) > MaxNativeOrderPointPositions {
		return false
	}
	seen := make(map[string]bool, len(positions))
	for _, position := range positions {
		if !validNativePosition(position) || position.VATEnabled.Validate() != nil || position.CompositionSafe.Validate() != nil || seen[position.ID] {
			return false
		}
		seen[position.ID] = true
	}
	return true
}
