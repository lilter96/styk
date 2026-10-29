// Package orderactions separates provider status facts from explicit commands.
package orderactions

import (
	"encoding/json"

	"github.com/lilter96/styk/internal/orders"
)

type Diagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Field    string `json:"field,omitempty"`
}

// Endpoint is diagnostic until Allowed is true. An unverified contract never
// obtains a body or dispatch permission merely because a transition is listed.
type RequestPlan struct {
	Scope          orders.Scope    `json:"scope"`
	Action         orders.Action   `json:"action"`
	CommandID      string          `json:"command_id"`
	PostingNumber  string          `json:"posting_number"`
	SourceRevision string          `json:"source_revision"`
	Endpoint       string          `json:"endpoint"`
	Method         string          `json:"method"`
	ContractHash   string          `json:"contract_hash,omitempty"`
	Body           json.RawMessage `json:"-"`
	Diagnostics    []Diagnostic    `json:"diagnostics"`
	Allowed        bool            `json:"allowed"`
}
