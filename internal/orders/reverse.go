package orders

// ReverseStatusMapping is an explicit, reviewed opt-in that proposes one Ozon
// fulfillment action when the linked MoySklad order reaches a native state.
// It never dispatches on its own: the proposal must still pass the normal
// preview and approval journey with operator-supplied evidence.
type ReverseStatusMapping struct {
	StateID string `json:"state_id"`
	Action  Action `json:"action"`
}

func (mapping ReverseStatusMapping) Validate() error {
	if !validUUID(mapping.StateID) {
		return ErrInvalid
	}
	switch mapping.Action {
	case ActionPostingPack, ActionPostingShip, ActionPostingDeliver, ActionPostingCancel:
		return nil
	default:
		return ErrInvalid
	}
}

// ReverseSuggestion is a read-only proposal derived from the current native
// MoySklad state. Empty ProposedAction means no reviewed mapping applies.
type ReverseSuggestion struct {
	Scope              Scope         `json:"scope"`
	PostingNumber      string        `json:"posting_number"`
	CommercialOrderID  string        `json:"commercial_order_id"`
	CustomerOrderID    string        `json:"customer_order_id"`
	MoySkladStateID    string        `json:"moysklad_state_id"`
	ProposedAction     Action        `json:"proposed_action,omitempty"`
	ParametersRequired []string      `json:"parameters_required"`
	ExpectedStatus     string        `json:"expected_status,omitempty"`
	PostingStatus      Field[string] `json:"posting_status"`
	Substatus          Field[string] `json:"substatus"`
	SourceRevision     string        `json:"source_revision"`
	TargetRevision     string        `json:"target_revision"`
	ConfigRevision     int64         `json:"config_revision"`
	Blockers           []Validation  `json:"blockers"`
	Ready              bool          `json:"ready"`
}

// ReverseSuggestionRequest identifies one linked posting to inspect.
type ReverseSuggestionRequest struct {
	Scope         Scope  `json:"scope"`
	PostingNumber string `json:"posting_number"`
}

// ReverseTransitionAllowed reports whether Ozon can accept the reverse-mapped
// action in the observed posting status. Unknown statuses are never allowed.
func ReverseTransitionAllowed(action Action, status string) bool {
	switch action {
	case ActionPostingPack:
		return status == "awaiting_packaging"
	case ActionPostingShip:
		return status == "awaiting_deliver"
	case ActionPostingDeliver:
		return status == "driver_pickup"
	case ActionPostingCancel:
		return status == "awaiting_packaging" || status == "awaiting_deliver"
	default:
		return false
	}
}
