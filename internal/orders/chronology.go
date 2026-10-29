package orders

type EvidenceOrder string

const (
	EvidenceSame     EvidenceOrder = "same"
	EvidenceNewer    EvidenceOrder = "newer"
	EvidenceOlder    EvidenceOrder = "older"
	EvidenceConflict EvidenceOrder = "conflict"
	EvidenceUnknown  EvidenceOrder = "unknown"
)

// CompareEvidence compares next with previous. Opaque provider versions are only
// comparable for equality. Local ObservedAt never establishes provider ordering.
func CompareEvidence(previous, next EvidenceRevision) EvidenceOrder {
	if previous.Validate() != nil || next.Validate() != nil {
		return EvidenceUnknown
	}
	if previous.Origin != next.Origin {
		return EvidenceUnknown
	}
	if previous.Revision == next.Revision {
		return EvidenceSame
	}
	if sameProviderVersion(previous, next) {
		return EvidenceConflict
	}
	if !previous.ProviderUpdatedAt.IsKnown() || !next.ProviderUpdatedAt.IsKnown() {
		return EvidenceUnknown
	}
	before, after := *previous.ProviderUpdatedAt.Value, *next.ProviderUpdatedAt.Value
	if after.After(before) {
		return EvidenceNewer
	}
	if after.Before(before) {
		return EvidenceOlder
	}
	return EvidenceConflict
}

func sameProviderVersion(previous, next EvidenceRevision) bool {
	return previous.ProviderVersion.IsKnown() && next.ProviderVersion.IsKnown() &&
		*previous.ProviderVersion.Value == *next.ProviderVersion.Value
}

// CanTransition never returns uncertain work to a write-capable state.
func CanTransition(from, to State) bool {
	if from == to {
		return from == StatePolling || from == StateAcknowledged || from == StateUnknown
	}
	switch from {
	case StateReady:
		return to == StateQueued || to == StateBlocked
	case StateQueued:
		return to == StateClaimed || to == StateBlocked
	case StateClaimed:
		return to == StateDispatching || to == StateQueued || to == StateBlocked || to == StateReconciled
	case StateDispatching:
		return to == StateAcknowledged || to == StatePolling || to == StateUnknown || to == StateRejected
	case StateAcknowledged, StatePolling:
		return to == StatePolling || to == StateAcknowledged || to == StateReconciled || to == StateUnknown || to == StateRejected
	case StateUnknown:
		return to == StatePolling || to == StateReconciled || to == StateRejected
	default:
		return false
	}
}

func (state State) ReadOnly() bool {
	return state == StateAcknowledged || state == StatePolling || state == StateUnknown
}

func (state State) Terminal() bool {
	return state == StateReconciled || state == StateRejected || state == StateBlocked
}
