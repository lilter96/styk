package orders

import "time"

const MaxNativeOrderPointPositions = 1000

// NativeOrderPoint is one complete, exact customerorder read. Complete means
// every native position was enumerated, not that an account, order window or
// stock snapshot was covered. These local read clocks are not provider versions.
// No buyer fields, document description or arbitrary attributes are retained.
type NativeOrderPoint struct {
	Scope              Scope            `json:"scope"`
	Correlation        Correlation      `json:"correlation"`
	CredentialRevision int64            `json:"credential_revision"`
	StartedAt          time.Time        `json:"started_at"`
	FinishedAt         time.Time        `json:"finished_at"`
	ObservedAt         time.Time        `json:"observed_at"`
	TargetRevision     string           `json:"target_revision"`
	StoreID            string           `json:"store_id"`
	StateID            string           `json:"state_id"`
	Applicable         Field[bool]      `json:"applicable"`
	Moment             Field[time.Time] `json:"moment"`
	// The provider's timezone-less native timestamp remains an exact string fact.
	ProviderUpdatedAt Field[string]    `json:"provider_updated_at,omitzero"`
	SourceDeleted     Field[string]    `json:"source_deleted,omitzero"`
	Positions         []TargetPosition `json:"positions"`
}

func NewNativeOrderPoint(target TargetObservation, credentialRevision int64, startedAt, finishedAt time.Time) (NativeOrderPoint, error) {
	if !target.Complete || len(target.Positions) > MaxNativeOrderPointPositions {
		return NativeOrderPoint{}, ErrBlocked
	}
	point := NativeOrderPoint{Scope: target.Scope, Correlation: target.Correlation, CredentialRevision: credentialRevision, StartedAt: startedAt, FinishedAt: finishedAt, ObservedAt: target.ObservedAt, TargetRevision: target.Revision, StoreID: target.Fields.StoreID, StateID: target.Fields.StateID, Applicable: nativePointFieldCopy(target.Fields.Applicable), Moment: nativePointFieldCopy(target.Fields.Moment), ProviderUpdatedAt: nativePointFieldCopy(target.ProviderUpdatedAt), Positions: nativePointPositions(target.Positions)}
	point.SourceDeleted = nativePointFieldCopy(target.SourceDeleted)
	if err := point.Validate(); err != nil {
		return NativeOrderPoint{}, err
	}
	return point, nil
}

func (point NativeOrderPoint) Validate() error {
	if point.Scope.Validate() != nil || point.Correlation.Validate() != nil || point.Correlation.Scope != point.Scope || point.Correlation.CustomerOrderID == "" || point.CredentialRevision <= 0 || !validID(point.TargetRevision) || !validID(point.StoreID) {
		return ErrInvalid
	}
	if point.StateID != "" && !validID(point.StateID) || !nativePointInterval(point.StartedAt, point.FinishedAt, point.ObservedAt) {
		return ErrInvalid
	}
	if point.Applicable.Validate() != nil || point.Moment.Validate() != nil || point.Moment.IsKnown() && point.Moment.Value.IsZero() || point.ProviderUpdatedAt.Validate() != nil || point.ProviderUpdatedAt.IsKnown() && validText(*point.ProviderUpdatedAt.Value) != nil {
		return ErrInvalid
	}
	if point.SourceDeleted.Validate() != nil || point.SourceDeleted.IsKnown() && validText(*point.SourceDeleted.Value) != nil {
		return ErrInvalid
	}
	if len(point.Positions) > MaxNativeOrderPointPositions {
		return ErrBlocked
	}
	seen := make(map[string]bool, len(point.Positions))
	for _, position := range point.Positions {
		if !validNativePosition(position) || position.VATEnabled.Validate() != nil || position.CompositionSafe.Validate() != nil || seen[position.ID] {
			return ErrInvalid
		}
		seen[position.ID] = true
	}
	return nil
}

func nativePointInterval(started, finished, observed time.Time) bool {
	return !started.IsZero() && !finished.IsZero() && !observed.IsZero() && !finished.Before(started) && !observed.Before(started) && !observed.After(finished)
}

func nativePointPositions(positions []TargetPosition) []TargetPosition {
	result := make([]TargetPosition, len(positions))
	for i, position := range positions {
		position.Reserve, position.Shipped = nativePointFieldCopy(position.Reserve), nativePointFieldCopy(position.Shipped)
		position.Discount, position.VAT = nativePointFieldCopy(position.Discount), nativePointFieldCopy(position.VAT)
		position.Price = nativePointFieldCopy(position.Price)
		position.VATEnabled, position.CompositionSafe = nativePointFieldCopy(position.VATEnabled), nativePointFieldCopy(position.CompositionSafe)
		result[i] = position
	}
	return result
}

func nativePointFieldCopy[T any](field Field[T]) Field[T] {
	if field.Value != nil {
		value := *field.Value
		field.Value = &value
	}
	return field
}
