// Package orderinventory attributes observed marketplace obligations to the
// existing source baseline. It never calculates available stock or writes stock.
package orderinventory

import (
	"fmt"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/orders"
)

const AttributionPending = "ORDER_OBLIGATION_ATTRIBUTION_PENDING"

type State string

const (
	SourceObserved    State = "source_observed"
	Unreflected       State = "unreflected"
	ProjectionPending State = "projection_pending"
	ReserveObserved   State = "reserve_observed"
	BaselineMatched   State = "baseline_matched"
	Shipped           State = "shipped"
	Cancelled         State = "cancelled"
	Unknown           State = "unknown"
)

// Observation preserves native facts separately from the correlation binding.
// A resolved binding or a successful mirror alone cannot prove stock coverage.
type Observation struct {
	Posting          orders.Posting
	Lines            []orders.LineIdentity
	Binding          orders.Correlation
	Work             *orders.Work
	Target           *orders.TargetObservation
	Positions        []domain.StockObservedOrderPosition
	NativeSyncID     *string
	NativeStartedAt  time.Time
	NativeFinishedAt time.Time
	Quarantined      bool
	Truncated        bool
}

type Input struct {
	Pool              domain.StockPool
	Baseline          domain.StockSourceEvidence
	BaselineStartedAt time.Time
	Now               time.Time
	MaxAge            time.Duration
	Observations      []Observation
	Truncated         bool
}

type Obligation struct {
	Scope           orders.Scope `json:"scope"`
	PostingNumber   string       `json:"posting_number"`
	CustomerOrderID string       `json:"customer_order_id,omitempty"`
	Quantity        string       `json:"quantity,omitempty"`
	Reserve         string       `json:"reserve,omitempty"`
	State           State        `json:"state"`
	Reason          string       `json:"reason,omitempty"`
}

type Guard struct {
	Obligations []Obligation `json:"obligations"`
	Truncated   bool         `json:"truncated"`
	Blocked     bool         `json:"blocked"`
}

// Apply preserves the authoritative calculator's arithmetic. In particular,
// matched reserves remain deducted only by Source.Reserve. Pending attribution
// cannot be bypassed by an operator's zero unreflected-obligation assumption.
func (guard Guard) Apply(decision *domain.StockDecision) {
	if len(guard.Obligations) == 0 && !guard.Truncated {
		return
	}
	decision.Checks = append(decision.Checks, domain.StockCheck{Code: AttributionPending, Passed: !guard.Blocked, Message: "Observed order obligations have native reservation attribution in the selected stock baseline.", Evidence: guard.reference()})
	if !guard.Blocked {
		return
	}
	decision.Status, decision.Quantity, decision.ExpiresAt = domain.StockBlocked, nil, nil
	if !contains(decision.ReasonCodes, AttributionPending) {
		decision.ReasonCodes = append(decision.ReasonCodes, AttributionPending)
	}
}

func (guard Guard) reference() string {
	var result strings.Builder
	for _, obligation := range guard.Obligations {
		fmt.Fprintf(&result, "%s/%s:%s(%s);", obligation.Scope.OzonAccount, obligation.PostingNumber, obligation.State, obligation.Reason)
	}
	if guard.Truncated {
		result.WriteString("bounded_evidence_truncated;")
	}
	return result.String()
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
