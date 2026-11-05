package orders

import (
	"encoding/json"
	"slices"
	"time"
)

// Admission chronology comes from PostgreSQL, not source/provider timestamps.
// SoleNativeAdmission excludes every other historical or pending parent mirror.
type RetainedSplitAdmissionFacts struct {
	Split                                                                      Work
	Before, Parent, Child, AdmissionSource                                     Posting
	Children                                                                   []Posting
	OriginalCorrelation                                                        Correlation
	Admission                                                                  Work
	Target                                                                     TargetObservation
	SplitReconciledAt, NativeCreatedAt, NativeDispatchedAt, NativeReconciledAt time.Time
	SoleNativeAdmission                                                        bool
}

// NewRetainedSplitAdmission proves a root first created natively AFTER a split
// already reserved only its retained allocation. No reduction is invented, and
// stock-baseline attribution remains the separate commitment-engine decision.
func NewRetainedSplitAdmission(f RetainedSplitAdmissionFacts) (SplitReserveTransferPermission, error) {
	if !retainedAdmissionSplit(f) || !retainedAdmissionNative(f) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	proof := SplitReserveTransferPermission{Scope: f.Before.Scope, SplitWorkID: f.Split.ID, ParentWorkID: f.Admission.ID, PostingNumber: f.Child.PostingNumber, ParentPosting: splitTransferSafePosting(f.Parent), ChildPosting: splitTransferSafePosting(f.Child), ParentCorrelation: f.Target.Correlation, ParentTargetRevision: f.Target.Revision, ParentStoreID: f.Target.Fields.StoreID, ParentOrganizationID: f.Target.Fields.OrganizationID, ParentPositions: slices.Clone(f.Target.Positions), ConfirmedAt: f.Target.ObservedAt}
	if !proof.Allows(f.Child) || !splitTransferChildReservable(f.Child) {
		return SplitReserveTransferPermission{}, ErrBlocked
	}
	raw, err := json.Marshal(proof)
	if err != nil {
		return SplitReserveTransferPermission{}, ErrInvalid
	}
	var frozen SplitReserveTransferPermission
	if json.Unmarshal(raw, &frozen) != nil {
		return SplitReserveTransferPermission{}, ErrInvalid
	}
	return frozen, nil
}

func retainedAdmissionSplit(f RetainedSplitAdmissionFacts) bool {
	w, b, c := f.Split, f.Before, f.Split.Command
	if !f.SoleNativeAdmission || !lineagePostingTrustworthy(b) || b.ParentPostingNumber.IsKnown() || len(b.ChildPostingNumbers) > 0 || w.Scope != b.Scope || w.State != StateReconciled || w.DispatchCount != 1 || !validID(w.ID) {
		return false
	}
	if c.Action != ActionPostingSplitUnassembled || c.PostingAction == nil || c.Scope != b.Scope || c.PostingNumber != b.PostingNumber || c.CommercialOrderID != b.CommercialOrderID || c.ExpectedSourceRevision != b.Evidence.Revision {
		return false
	}
	if f.OriginalCorrelation.Validate() != nil || f.OriginalCorrelation.CustomerOrderID != "" || c.Correlation != f.OriginalCorrelation || f.OriginalCorrelation.Scope != b.Scope || f.OriginalCorrelation.PostingNumber != b.PostingNumber {
		return false
	}
	if w.Receipt.State != StateReconciled || !unassembledAcknowledgedAllocation(w) || !slices.Contains(w.Receipt.PostingNumbers, b.PostingNumber) {
		return false
	}
	for _, member := range w.Receipt.ObservedPostings {
		if !splitTransferProductIdentities(b.Lines, member.Lines) {
			return false
		}
	}
	current := UnmirroredUnassembledSplitFacts{Split: w, Before: b, Parent: f.Parent, Children: f.Children}
	allocation := SplitReserveTransferFacts{Split: w, Before: b, Parent: f.Parent, Child: f.Child}
	return validUnmirroredUnassembledCurrent(current) && splitTransferAllocationVerified(allocation) && SplitObservedForAction(c.Action, b, c.PostingAction.Parameters.Packages, w.Receipt.PostingNumbers, w.Receipt.ObservedPostings)
}

func retainedAdmissionNative(f RetainedSplitAdmissionFacts) bool {
	w, c, t := f.Admission, f.Admission.Command, f.Target
	if !retainedAdmissionChronology(f) || w.State != StateReconciled || w.DispatchCount != 1 || w.Scope != f.Before.Scope || !validID(w.ID) || w.ID == f.Split.ID {
		return false
	}
	if c.Validate() != nil || c.Action != ActionMirrorCreate || c.Scope != w.Scope || c.CustomerOrder == nil || c.PostingNumber != f.Parent.PostingNumber || c.CommercialOrderID != f.Before.CommercialOrderID || c.Correlation != f.OriginalCorrelation || !supersededTransferWarehouse(c.Warehouse, f.Split.Command.Warehouse) {
		return false
	}
	if !lineagePostingTrustworthy(f.AdmissionSource) || c.ExpectedSourceRevision != f.AdmissionSource.Evidence.Revision || !splitTransferSameAllocation(f.AdmissionSource, f.Parent) {
		return false
	}
	receipt := w.Receipt
	if receipt.State != StateAcknowledged || receipt.CommandID != c.ID || receipt.HTTPStatus < 200 || receipt.HTTPStatus >= 300 || receipt.ExternalID == "" || receipt.ExternalID != t.Correlation.CustomerOrderID {
		return false
	}
	if t.ObservedAt.Before(f.NativeDispatchedAt.Add(-5*time.Second)) || t.ObservedAt.Before(receipt.ObservedAt) {
		return false
	}
	if !t.Complete || t.Scope != w.Scope || t.ObservedAt.IsZero() || !validID(t.Revision) || t.SourceDeleted.Validate() != nil || t.SourceDeleted.IsKnown() && *t.SourceDeleted.Value != "" || !t.Fields.Applicable.IsKnown() || !*t.Fields.Applicable.Value || !compositionReferencesMatch(c, t) || len(t.Positions) != len(c.CustomerOrder.Positions) {
		return false
	}
	baseline, err := ProjectionPositionBaseline(w, t)
	return err == nil && baseline.RecoveryID == "" && baseline.EvidenceClass != "OPERATOR_ATTESTED" && splitTransferAllParentLinesReserved(f.Parent, baseline)
}

// Native target clocks are sanity-bounded (5s skew) against dispatch, not used
// as a provider version or stock watermark. Exact UUID/ACK and immutable DB
// reconciliation remain mandatory evidence.
func retainedAdmissionChronology(f RetainedSplitAdmissionFacts) bool {
	return !f.SplitReconciledAt.IsZero() && !f.NativeCreatedAt.IsZero() && !f.NativeDispatchedAt.IsZero() && !f.NativeReconciledAt.IsZero() && !f.NativeCreatedAt.Before(f.SplitReconciledAt) && !f.NativeDispatchedAt.Before(f.NativeCreatedAt) && !f.NativeReconciledAt.Before(f.NativeDispatchedAt)
}
