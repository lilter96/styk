package orders

import (
	"encoding/json"
	"slices"
	"time"
)

// ScalarAdmissionWitness is reconstructed from actual immutable work and its
// scoped reconciliation. It is server-only evidence, never browser input. SQL
// compares both full position frontiers to the root creation, including foreign
// positions; it never derives ownership from a SKU. NoWriteIntent distinguishes
// a recorded read-only reconciliation from an acknowledged external mutation.
type ScalarAdmissionWitness struct {
	WorkID, CommandID, Fingerprint                     string
	Scope, CommandScope                                Scope
	Action, CommandAction                              Action
	State                                              State
	DispatchCount                                      int
	PostingNumber, CommercialOrderID                   string
	Correlation                                        Correlation
	BaselineWorkID, BaselineCommandID                  string
	ReceiptState                                       State
	HTTPStatus                                         int
	ReceiptCommandID, ExternalID                       string
	CreatedAt, DispatchedAt, ReconciledAt              time.Time
	PositionFrontierUnchanged, NativeIdentityUnchanged bool
	NoWriteIntent                                      bool
	ObservedAt                                         time.Time
}

type RetainedSplitRefreshFacts struct {
	Root          RetainedSplitAdmissionFacts
	Refresh       Work
	RefreshSource Posting
	Target        TargetObservation
	History       []ScalarAdmissionWitness
}

// NewRetainedSplitAdmissionRefresh permits acknowledged scalar updates or
// explicitly proven read-only reconciliations whose entire position frontier
// still belongs to the original creation. It
// never calls the sole-admission constructor with a fabricated sole-history flag.
func NewRetainedSplitAdmissionRefresh(f RetainedSplitRefreshFacts) (SplitReserveTransferPermission, error) {
	if !retainedAdmissionSplit(f.Root) || !retainedAdmissionNative(f.Root) || !validScalarAdmissionHistory(f) || !validScalarAdmissionRefresh(f) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	p := SplitReserveTransferPermission{Scope: f.Root.Before.Scope, SplitWorkID: f.Root.Split.ID, ParentWorkID: f.Refresh.ID, PostingNumber: f.Root.Child.PostingNumber, ParentPosting: splitTransferSafePosting(f.Root.Parent), ChildPosting: splitTransferSafePosting(f.Root.Child), ParentCorrelation: f.Target.Correlation, ParentTargetRevision: f.Target.Revision, ParentStoreID: f.Target.Fields.StoreID, ParentOrganizationID: f.Target.Fields.OrganizationID, ParentPositions: slices.Clone(f.Target.Positions), ConfirmedAt: f.Target.ObservedAt}
	if !p.Allows(f.Root.Child) || !splitTransferChildReservable(f.Root.Child) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return SplitReserveTransferPermission{}, ErrInvalid
	}
	var frozen SplitReserveTransferPermission
	if json.Unmarshal(raw, &frozen) != nil {
		return SplitReserveTransferPermission{}, ErrInvalid
	}
	return frozen, nil
}

func validScalarAdmissionHistory(f RetainedSplitRefreshFacts) bool {
	if len(f.History) == 0 || len(f.History) > 1024 {
		return false
	}
	seen := make(map[string]bool, len(f.History))
	previous := f.Root.NativeReconciledAt
	for _, step := range f.History {
		if seen[step.WorkID] || !validScalarAdmissionStep(f.Root, step, previous) {
			return false
		}
		seen[step.WorkID] = true
		previous = step.ReconciledAt
	}
	last := f.History[len(f.History)-1]
	return last.WorkID == f.Refresh.ID && last.CommandID == f.Refresh.Command.ID && last.Fingerprint == f.Refresh.CommandFingerprint
}

func validScalarAdmissionStep(root RetainedSplitAdmissionFacts, s ScalarAdmissionWitness, previous time.Time) bool {
	if !validID(s.WorkID) || !validID(s.CommandID) || !validID(s.Fingerprint) || s.WorkID == root.Admission.ID || s.Scope != root.Before.Scope || s.CommandScope != s.Scope || s.Action != ActionMirrorUpdate || s.CommandAction != s.Action || s.State != StateReconciled || s.DispatchCount < 0 || s.DispatchCount > 1 {
		return false
	}
	if s.PostingNumber != root.Parent.PostingNumber || s.CommercialOrderID != root.Before.CommercialOrderID || s.Correlation != root.Target.Correlation || s.BaselineWorkID != root.Admission.ID || s.BaselineCommandID != root.Admission.Command.ID {
		return false
	}
	if s.CreatedAt.Before(root.NativeCreatedAt) || !s.PositionFrontierUnchanged || !s.NativeIdentityUnchanged || s.ObservedAt.IsZero() || s.ObservedAt.Before(s.CreatedAt.Add(-5*time.Second)) {
		return false
	}
	return validScalarAdmissionDispatch(s, root.Target.Correlation.CustomerOrderID, previous)
}

func validScalarAdmissionDispatch(s ScalarAdmissionWitness, nativeID string, previous time.Time) bool {
	if s.CreatedAt.IsZero() || s.ReconciledAt.IsZero() || s.ReconciledAt.Before(s.CreatedAt) || s.ReconciledAt.Before(previous) {
		return false
	}
	if s.DispatchCount == 0 {
		return s.NoWriteIntent && s.DispatchedAt.IsZero() && s.ReceiptState == "" && s.HTTPStatus == 0 && s.ReceiptCommandID == "" && s.ExternalID == ""
	}
	return !s.NoWriteIntent && s.ReceiptState == StateAcknowledged && s.HTTPStatus >= 200 && s.HTTPStatus < 300 && s.ReceiptCommandID == s.CommandID && s.ExternalID == nativeID && !s.DispatchedAt.IsZero() && !s.DispatchedAt.Before(s.CreatedAt) && !s.DispatchedAt.Before(previous) && !s.ReconciledAt.Before(s.DispatchedAt) && !s.ObservedAt.Before(s.DispatchedAt.Add(-5*time.Second))

}

func validScalarAdmissionRefresh(f RetainedSplitRefreshFacts) bool {
	w, c, t := f.Refresh, f.Refresh.Command, f.Target
	if w.State != StateReconciled || (w.DispatchCount != 0 && w.DispatchCount != 1) || w.Scope != f.Root.Before.Scope || c.Validate() != nil || c.Action != ActionMirrorUpdate || c.Scope != w.Scope || c.CustomerOrder == nil || c.PositionBaseline == nil || c.Correlation != f.Root.Target.Correlation {
		return false
	}
	if c.PostingNumber != f.Root.Parent.PostingNumber || c.CommercialOrderID != f.Root.Before.CommercialOrderID || !lineagePostingTrustworthy(f.RefreshSource) || c.ExpectedSourceRevision != f.RefreshSource.Evidence.Revision || !splitTransferSameAllocation(f.RefreshSource, f.Root.Parent) {
		return false
	}
	base, err := ProjectionPositionBaseline(f.Root.Admission, f.Root.Target)
	if err != nil || !base.Equal(*c.PositionBaseline) || !ScalarPositionFrontier(c, base, t.Positions) || !NativePositionsEqual(f.Root.Target.Positions, c.TargetPositions) || !NativePositionsEqual(f.Root.Target.Positions, t.Positions) {
		return false
	}
	if !validScalarAdmissionTarget(f, c, t) {
		return false
	}
	fingerprint, err := CommandFingerprint(c)
	if err != nil || fingerprint != w.CommandFingerprint {
		return false
	}
	receipt := w.Receipt
	if w.DispatchCount == 0 {
		return receipt.State == "" && receipt.HTTPStatus == 0 && receipt.CommandID == "" && receipt.ExternalID == ""
	}
	return receipt.State == StateAcknowledged && receipt.CommandID == c.ID && receipt.HTTPStatus >= 200 && receipt.HTTPStatus < 300 && receipt.ExternalID == t.Correlation.CustomerOrderID && !t.ObservedAt.Before(receipt.ObservedAt)
}

func validScalarAdmissionTarget(f RetainedSplitRefreshFacts, c Command, t TargetObservation) bool {
	last := f.History[len(f.History)-1]
	if t.Scope != f.Root.Before.Scope || !t.Complete || t.Correlation != f.Root.Target.Correlation || t.ObservedAt.IsZero() || !validID(t.Revision) || t.SourceDeleted.Validate() != nil || t.SourceDeleted.IsKnown() && *t.SourceDeleted.Value != "" {
		return false
	}
	if !t.Fields.Applicable.IsKnown() || !*t.Fields.Applicable.Value || !compositionReferencesMatch(c, t) || !compositionReferencesMatch(f.Root.Admission.Command, t) || !supersededTransferWarehouse(c.Warehouse, f.Root.Admission.Command.Warehouse) {
		return false
	}
	if last.DispatchCount == 0 {
		return !t.ObservedAt.Before(last.CreatedAt.Add(-5 * time.Second))
	}
	return !t.ObservedAt.Before(last.DispatchedAt.Add(-5 * time.Second))
}
