package orders

import (
	"slices"
	"time"
)

// NativeStateBaseline identifies a state actually written by STYK and confirmed
// by an exact native readback. A current state value alone is not provenance.
type NativeStateBaseline struct {
	Scope          Scope       `json:"scope"`
	Correlation    Correlation `json:"correlation"`
	WorkID         string      `json:"work_id"`
	CommandID      string      `json:"command_id"`
	StateID        string      `json:"state_id"`
	TargetRevision string      `json:"target_revision"`
	ObservedAt     time.Time   `json:"observed_at"`
}

func (baseline NativeStateBaseline) Validate() error {
	if baseline.Scope.Validate() != nil || baseline.Correlation.Validate() != nil || baseline.Correlation.Scope != baseline.Scope || baseline.Correlation.CustomerOrderID == "" {
		return ErrInvalid
	}
	if !validID(baseline.WorkID) || !validID(baseline.CommandID) || !validID(baseline.StateID) || !validID(baseline.TargetRevision) || baseline.ObservedAt.IsZero() {
		return ErrInvalid
	}
	return nil
}

func (baseline NativeStateBaseline) OwnsTarget(target TargetObservation) bool {
	return baseline.Validate() == nil && target.Complete && target.Scope == baseline.Scope && target.Correlation == baseline.Correlation &&
		target.Revision == baseline.TargetRevision && target.Fields.StateID == baseline.StateID && !target.ObservedAt.Before(baseline.ObservedAt)
}

func (baseline NativeStateBaseline) Equal(other NativeStateBaseline) bool {
	return baseline.Scope == other.Scope && baseline.Correlation == other.Correlation && baseline.WorkID == other.WorkID && baseline.CommandID == other.CommandID &&
		baseline.StateID == other.StateID && baseline.TargetRevision == other.TargetRevision && baseline.ObservedAt.Equal(other.ObservedAt)
}

func ProjectionNativeStateBaseline(work Work, target TargetObservation) (NativeStateBaseline, error) {
	command, receipt := work.Command, work.Receipt
	if work.State != StateReconciled || work.DispatchCount != 1 || command.CustomerOrder == nil || work.Scope != command.Scope || work.Scope != target.Scope {
		return NativeStateBaseline{}, ErrBlocked
	}
	if command.Action != ActionMirrorCreate && (command.Action != ActionMirrorUpdate || !slices.Contains(command.CustomerOrder.OwnedFields, "state")) {
		return NativeStateBaseline{}, ErrBlocked
	}
	if (receipt.State != StateAcknowledged && receipt.State != StateReconciled) || receipt.CommandID != command.ID || receipt.HTTPStatus < 200 || receipt.HTTPStatus >= 300 || receipt.ExternalID != target.Correlation.CustomerOrderID || receipt.ObservedAt.IsZero() || receipt.ObservedAt.After(target.ObservedAt) {
		return NativeStateBaseline{}, ErrBlocked
	}
	if !target.Complete || target.Fields.StateID != command.CustomerOrder.StateID || target.Correlation.PostingNumber != command.PostingNumber || !baselineCorrelationMatches(command.Correlation, target.Correlation) {
		return NativeStateBaseline{}, ErrStale
	}
	result := NativeStateBaseline{Scope: work.Scope, Correlation: target.Correlation, WorkID: work.ID, CommandID: command.ID,
		StateID: target.Fields.StateID, TargetRevision: target.Revision, ObservedAt: target.ObservedAt}
	return result, result.Validate()
}
