package orders

import "time"

// Bound the complete native event history, including staged row reductions.
// Each reduction must prove its exact preceding reconciled UUID frontier.
const MaxSplitForestNative = MaxSplitLineageHeads + MaxSplitLineageSteps

// SplitForestNative is one actual, immutable native operation and its approved
// source. Attempt clocks are database facts, not provider watermarks.
type SplitForestNative struct {
	Work    Work
	Source  Posting
	Target  TargetObservation
	Attempt NestedSplitAttempt
}

// SplitForestFacts is a complete scoped history, not a selected latest row.
// Current heads remain provider observations; historical prefixes never receive
// fabricated freshness or source revisions.
type SplitForestFacts struct {
	Lineage   SplitLineageFacts
	Attempts  []NestedSplitAttempt
	Native    []SplitForestNative
	CheckedAt time.Time
	Complete  bool
}

// NewSplitForestSettlement reconstructs reserve authority for mixed branches
// using the existing calculation and permission format. It issues no command.
func NewSplitForestSettlement(f SplitForestFacts) (SplitAncestorSettlementPermission, error) {
	if !forestFactsValid(f) {
		return SplitAncestorSettlementPermission{}, ErrBlocked
	}
	for _, event := range f.Native {
		if !forestNativeValid(f, event) {
			return SplitAncestorSettlementPermission{}, ErrBlocked
		}
	}
	return forestCurrentPermission(f)
}

func forestFactsValid(f SplitForestFacts) bool {
	if !f.Complete || f.CheckedAt.IsZero() || len(f.Native) == 0 || len(f.Native) > MaxSplitForestNative || !ancestorSettlementHeadsFresh(f.Lineage.CurrentHeads, f.CheckedAt) {
		return false
	}
	graph, err := buildSplitLineage(f.Lineage)
	if err != nil || !nestedSettlementAttempts(NestedSplitSettlementFacts{Lineage: f.Lineage, Attempts: f.Attempts, CheckedAt: f.CheckedAt}, graph) {
		return false
	}
	for _, step := range f.Lineage.Splits {
		if step.Work.Command.Action != ActionPostingSplitUnassembled || !unassembledAcknowledgedAllocation(step.Work) || ancestorNodeOutput(step.Work, step.Before.PostingNumber).PostingNumber != step.Before.PostingNumber {
			return false
		}
	}
	seen := make(map[string]bool, len(f.Native))
	origins, ids, postingIDs := make(map[string]bool), make(map[string]string), make(map[string]string)
	for _, event := range f.Native {
		number, id := event.Source.PostingNumber, event.Target.Correlation.CustomerOrderID
		if seen[event.Work.ID] || !forestNativeShape(f, event) || !forestNativeSplitClocks(f, event) || ids[id] != "" && ids[id] != number || postingIDs[number] != "" && postingIDs[number] != id {
			return false
		}
		if event.Work.Command.Action == ActionMirrorCreate {
			if origins[number] {
				return false
			}
			origins[number] = true
		}
		seen[event.Work.ID] = true
		ids[id] = number
		postingIDs[number] = id
	}
	return true
}

func forestNativeShape(f SplitForestFacts, e SplitForestNative) bool {
	w, c := e.Work, e.Work.Command
	_, clocks := deepAncestorTimes([]NestedSplitAttempt{e.Attempt}, f.CheckedAt)
	return clocks && !e.Attempt.ReconciledAt.After(f.CheckedAt) && e.Attempt.WorkID == w.ID && w.Scope == f.Lineage.Scope && c.Scope == f.Lineage.Scope && ancestorNativeAcknowledged(w, e.Target) && baselineCorrelationMatches(c.Correlation, e.Target.Correlation) && lineagePostingTrustworthy(e.Source) && e.Source.Scope == w.Scope && c.PostingNumber == e.Source.PostingNumber && c.CommercialOrderID == e.Source.CommercialOrderID && c.ExpectedSourceRevision == e.Source.Evidence.Revision && forestHead(f, c.PostingNumber).PostingNumber != ""
}
