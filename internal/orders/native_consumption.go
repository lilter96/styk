package orders

import "time"

type NativeParentEffect string

const (
	// The empty historical mode retains the existing exact shipped-delta proof.
	NativeParentShippedCounter   NativeParentEffect = ""
	NativeParentUnpostedDocument NativeParentEffect = "UNPOSTED_DOCUMENT_ZERO_COUNTER"
	MaxNativeConsumptionEvents                      = 128
	MaxNativeConsumptionBytes                       = 16 << 20
)

// NativeConsumptionFacts is a complete, bounded database history. It is review
// evidence, never a recursive command payload or proof of fresh provider state.
type NativeConsumptionFacts struct {
	Scope       Scope
	Correlation Correlation
	Complete    bool
	CheckedAt   time.Time
	Events      []NativeConsumptionEvent
}

type NativeConsumptionEvent struct {
	Work        Work
	Document    *NativeDocumentObservation
	Parent      *TargetObservation
	ConfirmedAt time.Time
	WriteIntent Field[bool]
}

// NativeConsumptionSeal freezes gross shipment consumption and its exact
// document identities. Returns remain separate and never authorize reshipping
// the same order. Digest excludes mutable traversal/observation timestamps.
type NativeConsumptionSeal struct {
	Digest    string                   `json:"digest"`
	Positions []NativeConsumedPosition `json:"positions"`
	Events    []NativeConsumedEvent    `json:"events"`
}

type NativeConsumedPosition struct {
	OrderPositionID string  `json:"order_position_id"`
	GrossDemand     Decimal `json:"gross_demand"`
}

type NativeConsumedEvent struct {
	CommandFingerprint string               `json:"command_fingerprint"`
	WorkID             string               `json:"work_id"`
	DocumentID         string               `json:"document_id"`
	Kind               string               `json:"kind"`
	DemandWorkID       string               `json:"demand_work_id,omitempty"`
	Lines              []NativeConsumedLine `json:"lines"`
}

type NativeConsumedLine struct {
	OrderPositionID string  `json:"order_position_id"`
	BasisPositionID string  `json:"basis_position_id,omitempty"`
	Quantity        Decimal `json:"quantity"`
}
