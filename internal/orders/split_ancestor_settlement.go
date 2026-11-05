package orders

import (
	"encoding/json"
	"slices"
	"time"
)

const (
	SplitAncestorReduce = "retained_reduction"
	SplitAncestorAdmit  = "child_admission"
	// The node has no native document yet. Baseline and NodeOriginWorkID refer
	// to its actual nearest native ancestor authority, never a fabricated node baseline.
	SplitAncestorNodeAdmit = "node_admission"
)

// SplitAncestorNative is an exact native frontier backed by a scoped immutable
// reconciliation. It is not an assertion that a reserve occurs in a stock batch.
type SplitAncestorNative struct {
	Posting Posting           `json:"posting"`
	WorkID  string            `json:"work_id"`
	Target  TargetObservation `json:"target"`
}

// SplitAncestorSettlementPermission is reconstructed at every local boundary.
// Its shape alone is never authority: the store compares it to actual history.
// Bounded retained generations use the same permit and dispatch path.
type SplitAncestorSettlementPermission struct {
	Scope               Scope                 `json:"scope"`
	Mode                string                `json:"mode"`
	SplitWorkID         string                `json:"split_work_id"`
	RootSplitWorkID     string                `json:"root_split_work_id"`
	NodeOriginWorkID    string                `json:"node_origin_work_id"`
	NodeReductionWorkID string                `json:"node_reduction_work_id,omitempty"`
	Before              Posting               `json:"before"`
	Source              Posting               `json:"source"`
	Node                Posting               `json:"node"`
	Baseline            PositionBaseline      `json:"baseline"`
	Ancestors           []SplitAncestorNative `json:"ancestors"`
	// SourceAncestors is the complete ordered root-to-immediate-source-parent
	// path. It excludes Source and carries no invented native ownership. Empty
	// preserves the legacy all-native path represented by Ancestors.
	SourceAncestors []Posting `json:"source_ancestors,omitempty"`
}

type SplitAncestorSettlementFacts struct {
	Forest              *SplitForestFacts
	Deferred            *SplitDeferredSettlementFacts
	RetainedHistory     []SplitRetainedGeneration
	Path                []SplitAncestorGeneration
	Attempts            []NestedSplitAttempt
	NativeAttempts      []NestedSplitAttempt
	Lineage             SplitLineageFacts
	RootTransfer        SplitReserveTransferFacts
	RootOrigin          Work
	RootOriginTarget    TargetObservation
	NodeSplit           SplitLineageStep
	NodeOrigin          Work
	NodeOriginTarget    TargetObservation
	NodeReduction       *Work
	NodeReductionTarget *TargetObservation
	CurrentNode         Posting
	Selected            Posting
	// Proven by scoped DB reconstruction; queued or UNKNOWN mirrors are included.
	HistoryUnambiguous bool
	CheckedAt          time.Time
}

func NewSplitAncestorSettlement(f SplitAncestorSettlementFacts) (SplitAncestorSettlementPermission, error) {
	if f.Forest != nil {
		return NewSplitForestSettlement(*f.Forest)
	}
	if f.Deferred != nil {
		return newDeferredSettlement(f)
	}
	if len(f.RetainedHistory) > 1 {
		return newRetainedHistorySettlement(f)
	}
	if len(f.Path) > 1 {
		return newDeepAncestorSettlement(f)
	}
	root, err := ancestorSettlementRoot(f)
	if err != nil {
		return SplitAncestorSettlementPermission{}, err
	}
	origin, err := ancestorSettlementOrigin(f, root)
	if err != nil {
		return SplitAncestorSettlementPermission{}, err
	}
	p := SplitAncestorSettlementPermission{Scope: f.Selected.Scope, Mode: SplitAncestorReduce, SplitWorkID: f.NodeSplit.Work.ID, RootSplitWorkID: f.RootTransfer.Split.ID, NodeOriginWorkID: f.NodeOrigin.ID, Before: minimalSupersededPosting(f.NodeSplit.Before), Source: minimalSupersededPosting(f.CurrentNode), Node: minimalSupersededPosting(f.CurrentNode), Baseline: origin, Ancestors: []SplitAncestorNative{{Posting: minimalSupersededPosting(f.RootTransfer.Parent), WorkID: f.RootTransfer.ParentRelease.ID, Target: f.RootTransfer.ParentTarget}, {Posting: minimalSupersededPosting(f.CurrentNode), WorkID: f.NodeOrigin.ID, Target: f.NodeOriginTarget}}}
	if f.NodeReduction != nil {
		if err = ancestorSettlementReduced(f, p); err != nil {
			return SplitAncestorSettlementPermission{}, err
		}
		baseline, e := ProjectionPositionBaseline(*f.NodeReduction, *f.NodeReductionTarget)
		if e != nil {
			return SplitAncestorSettlementPermission{}, e
		}
		p.Baseline, p.NodeReductionWorkID = baseline, f.NodeReduction.ID
		p.Ancestors[1].WorkID, p.Ancestors[1].Target = f.NodeReduction.ID, *f.NodeReductionTarget
	}
	if f.Selected.PostingNumber != f.CurrentNode.PostingNumber {
		if f.NodeReduction == nil || !splitTransferChildReservable(f.Selected) {
			return SplitAncestorSettlementPermission{}, ErrBlocked
		}
		p.Mode, p.Source = SplitAncestorAdmit, minimalSupersededPosting(f.Selected)
	}
	if p.Validate() != nil {
		return SplitAncestorSettlementPermission{}, ErrBlocked
	}
	return freezeAncestorSettlement(p)
}

func ancestorSettlementRoot(f SplitAncestorSettlementFacts) (SplitReserveTransferPermission, error) {
	if !f.HistoryUnambiguous || len(f.Lineage.Splits) < 2 || len(f.Lineage.CurrentHeads) < 3 || f.Lineage.Scope != f.Selected.Scope {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	if !ancestorSettlementHeadsFresh(f.Lineage.CurrentHeads, f.CheckedAt) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	graph, err := NewSplitLineage(f.Lineage)
	if err != nil || !graph.CurrentMatches(f.Selected) || !ancestorSettlementLineage(f, graph) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	rootBaseline, err := ProjectionPositionBaseline(f.RootOrigin, f.RootOriginTarget)
	prior, invalid := OwnedSplitReductionBaseline(f.RootTransfer.ParentRelease.Command)
	if err != nil || invalid != nil || !ancestorNativeAcknowledged(f.RootOrigin, f.RootOriginTarget) || !rootBaseline.Equal(prior) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	root, err := NewSplitReserveTransfer(f.RootTransfer)
	if err != nil || f.RootTransfer.Child.PostingNumber != f.NodeSplit.Before.PostingNumber || !splitTransferSameAllocation(f.RootTransfer.Child, f.NodeSplit.Before) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	if !ancestorSettlementNode(f) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	return root, nil
}

func ancestorNodeOutput(w Work, number string) Posting {
	for _, p := range w.Receipt.ObservedPostings {
		if p.PostingNumber == number {
			return p
		}
	}
	return Posting{}
}

func ancestorSettlementOrigin(f SplitAncestorSettlementFacts, root SplitReserveTransferPermission) (PositionBaseline, error) {
	w, t := f.NodeOrigin, f.NodeOriginTarget
	if w.Command.Action != ActionMirrorCreate || !ancestorNativeAcknowledged(w, t) || w.Command.SplitTransfer == nil || !w.Command.SplitTransfer.Equal(root) || w.Command.PostingNumber != f.NodeSplit.Before.PostingNumber || w.Command.ExpectedSourceRevision != f.NodeSplit.Before.Evidence.Revision {
		return PositionBaseline{}, ErrBlocked
	}
	baseline, err := ProjectionPositionBaseline(w, t)
	if err != nil || !splitTransferAllParentLinesReserved(f.NodeSplit.Before, baseline) {
		return PositionBaseline{}, ErrBlocked
	}
	return baseline, nil
}

func ancestorNativeAcknowledged(w Work, t TargetObservation) bool {
	c, r := w.Command, w.Receipt
	if w.State != StateReconciled || w.DispatchCount != 1 || c.Validate() != nil || t.Scope != w.Scope || t.Correlation.Scope != w.Scope || t.Correlation.PostingNumber != c.PostingNumber || !t.Complete || t.Revision == "" || t.ObservedAt.IsZero() || !nativeDocumentPresent(t.SourceDeleted) {
		return false
	}
	return r.State == StateAcknowledged && r.CommandID == c.ID && r.HTTPStatus >= 200 && r.HTTPStatus < 300 && r.ExternalID == t.Correlation.CustomerOrderID && r.ExternalID != "" && !t.ObservedAt.Before(r.ObservedAt) && compositionReferencesMatch(c, t) && t.Fields.Applicable.IsKnown() && *t.Fields.Applicable.Value
}

func ancestorSettlementReduced(f SplitAncestorSettlementFacts, before SplitAncestorSettlementPermission) error {
	w := f.NodeReduction
	if w == nil || f.NodeReductionTarget == nil {
		return ErrBlocked
	}
	prior, err := OwnedSplitReductionBaseline(w.Command)
	if err != nil || !ancestorNativeAcknowledged(*w, *f.NodeReductionTarget) || w.Command.SplitAncestorSettlement == nil || !w.Command.SplitAncestorSettlement.Equal(before) || !prior.Equal(before.Baseline) || !OwnedSplitReductionObserved(w.Command, *f.NodeReductionTarget) {
		return ErrBlocked
	}
	baseline, err := ProjectionPositionBaseline(*w, *f.NodeReductionTarget)
	if err != nil || !splitTransferAllParentLinesReserved(f.CurrentNode, baseline) {
		return ErrBlocked
	}
	return nil
}

func (p SplitAncestorSettlementPermission) Validate() error {
	if !p.sourceAncestorPathValid() {
		return ErrBlocked
	}
	if p.Mode == SplitAncestorNodeAdmit {
		return p.validateNodeAdmission()
	}
	if p.Scope.Validate() != nil || (p.Mode != SplitAncestorReduce && p.Mode != SplitAncestorAdmit) || !validID(p.SplitWorkID) || !validID(p.RootSplitWorkID) || !validID(p.NodeOriginWorkID) || p.SplitWorkID == p.RootSplitWorkID || len(p.Ancestors) < 1 || len(p.Ancestors) > MaxSplitLineageDepth || p.Baseline.Validate() != nil || p.Baseline.Scope != p.Scope {
		return ErrBlocked
	}
	if p.Before.Scope != p.Scope || p.Node.Scope != p.Scope || p.Source.Scope != p.Scope || p.Before.Validate() != nil || p.Node.Validate() != nil || p.Source.Validate() != nil || p.Before.PostingNumber != p.Node.PostingNumber || !retainedSplitSourceReduced(p.Before, p.Node, p.Baseline) {
		return ErrBlocked
	}
	seen := make(map[string]bool, len(p.Ancestors))
	nativeIDs := make(map[string]bool, len(p.Ancestors))
	for _, a := range p.Ancestors {
		if seen[a.Posting.PostingNumber] || nativeIDs[a.Target.Correlation.CustomerOrderID] || !validSplitAncestorNative(a, p.Scope) {
			return ErrBlocked
		}
		seen[a.Posting.PostingNumber] = true
		nativeIDs[a.Target.Correlation.CustomerOrderID] = true
	}
	if p.Mode == SplitAncestorReduce && p.Source.PostingNumber != p.Node.PostingNumber || p.Mode == SplitAncestorAdmit && (p.NodeReductionWorkID == "" || p.Source.PostingNumber == p.Node.PostingNumber || !ancestorParentConsistent(p.Source, p.Node.PostingNumber)) {
		return ErrBlocked
	}
	return nil
}

func (p SplitAncestorSettlementPermission) Allows(posting Posting) bool {
	return p.Validate() == nil && splitTransferPostingEqual(p.Source, minimalSupersededPosting(posting))
}

func (p SplitAncestorSettlementPermission) Equal(other SplitAncestorSettlementPermission) bool {
	if p.Scope != other.Scope || p.Mode != other.Mode || p.SplitWorkID != other.SplitWorkID || p.RootSplitWorkID != other.RootSplitWorkID || p.NodeOriginWorkID != other.NodeOriginWorkID || p.NodeReductionWorkID != other.NodeReductionWorkID || !p.Baseline.Equal(other.Baseline) || len(p.Ancestors) != len(other.Ancestors) || len(p.SourceAncestors) != len(other.SourceAncestors) {
		return false
	}
	for _, pair := range [][2]Posting{{p.Before, other.Before}, {p.Source, other.Source}, {p.Node, other.Node}} {
		if !splitTransferPostingEqual(pair[0], pair[1]) {
			return false
		}
	}
	for i, source := range p.SourceAncestors {
		if !splitTransferPostingEqual(source, other.SourceAncestors[i]) {
			return false
		}
	}
	for i, a := range p.Ancestors {
		b := other.Ancestors[i]
		if a.WorkID != b.WorkID || !splitTransferPostingEqual(a.Posting, b.Posting) || !a.TargetMatches(b.Target) || !a.Target.ObservedAt.Equal(b.Target.ObservedAt) {
			return false
		}
	}
	return true
}

func ApplyAncestorSplitOpenQuantityReserve(posting Posting, write *CustomerOrderWrite, baseline PositionBaseline, p SplitAncestorSettlementPermission) Validation {
	if write == nil || !p.Allows(posting) || write.StoreID != p.Ancestors[0].Target.Fields.StoreID || write.OrganizationID != p.Ancestors[0].Target.Fields.OrganizationID {
		return reservePolicyValidation("posting.lineage", "reservation_policy_ancestor_settlement_required")
	}
	if p.Mode == SplitAncestorReduce && !baseline.Equal(p.Baseline) {
		return reservePolicyValidation("posting.lineage", "reservation_policy_ancestor_frontier_changed")
	}
	if p.Mode == SplitAncestorNodeAdmit && (baseline.WorkID != "" || len(baseline.Bindings) != 0) {
		return reservePolicyValidation("posting.lineage", "reservation_policy_node_already_native")
	}
	candidate := *write
	candidate.Positions = slices.Clone(write.Positions)
	invalid := applyOpenQuantityReserve(posting, &candidate, baseline, true)
	if invalid.Code != "" {
		return invalid
	}
	if p.Mode == SplitAncestorReduce && !retainedSplitWriteReduced(candidate, baseline) {
		return reservePolicyValidation("positions.reserve", "reservation_policy_retained_split_reduction_required")
	}
	write.Positions = candidate.Positions
	return Validation{}
}

func (p SplitAncestorSettlementPermission) AllowsCommand(c Command) bool {
	if p.Mode == SplitAncestorNodeAdmit && (c.Action != ActionMirrorCreate || c.Correlation.CustomerOrderID != "") {
		return false
	}
	if !c.Action.IsMirror() || c.Scope != p.Scope || c.CustomerOrder == nil || c.PostingNumber != p.Source.PostingNumber || c.CommercialOrderID != p.Source.CommercialOrderID || c.ExpectedSourceRevision != p.Source.Evidence.Revision || !p.commandCorrelationMatches(c) {
		return false
	}
	candidate := *c.CustomerOrder
	candidate.Positions = slices.Clone(candidate.Positions)
	baseline := PositionBaseline{}
	if p.Mode == SplitAncestorReduce {
		baseline = p.Baseline
	}
	if ApplyAncestorSplitOpenQuantityReserve(p.Source, &candidate, baseline, p).Code != "" {
		return false
	}
	for i, line := range c.CustomerOrder.Positions {
		if !SameObservedDecimal(line.Reserve, candidate.Positions[i].Reserve) {
			return false
		}
	}
	return true
}

func freezeAncestorSettlement(p SplitAncestorSettlementPermission) (SplitAncestorSettlementPermission, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return SplitAncestorSettlementPermission{}, ErrInvalid
	}
	var frozen SplitAncestorSettlementPermission
	if json.Unmarshal(raw, &frozen) != nil {
		return frozen, ErrInvalid
	}
	return frozen, nil
}
