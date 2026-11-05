package orders

import "time"

// PostingTargetState is a bounded local execution check, not fresh provider
// evidence. Historical UNKNOWN outcomes are preserved even after their target
// fence is explicitly superseded; Unresolved describes the current fence only.
type PostingTargetState struct {
	Scope           Scope     `json:"scope"`
	PostingNumber   string    `json:"posting_number"`
	HasOperation    bool      `json:"has_operation"`
	State           State     `json:"state,omitempty"`
	Unresolved      bool      `json:"unresolved"`
	CustomerOrderID string    `json:"customer_order_id,omitempty"`
	SourceChanged   bool      `json:"source_changed"`
	CheckedAt       time.Time `json:"checked_at"`
}
