package domain

import "time"

// PostingLine contains only source identifiers, never inferred marketplace product IDs.
type PostingLine struct {
	OfferID, SKU string
	Quantity     int64
}

type PostingCancellation struct {
	Reason             *string
	ReasonID           *int64
	Type               *string
	Initiator          *string
	CancelledAfterShip *bool
}

type PostingObservation struct {
	PostingNumber, OrderID, OrderNumber, Status, Substatus, WarehouseID string
	// SourceInProcessAt is when processing began, not creation or last update.
	InProcessAt, ShipmentDate, DeliveringDate                       string
	ParentPostingNumber, FactDeliveryDate, ShipmentDateWithoutDelay string
	// No documented general source creation or update timestamp is currently exposed.
	SourceCreatedAt, SourceUpdatedAt *time.Time
	ObservedAt                       time.Time
	ObservedTimeBasis                string
	CancellationPresenceKnown        bool
	Cancellation                     *PostingCancellation
	Lines                            []PostingLine
}

type PostingImport struct {
	Tenant, Account                     string
	Since, Until, StartedAt, FinishedAt time.Time
	Postings                            []PostingObservation
}
