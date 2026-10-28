package domain

import "time"

// CustomerOrderPosition is an observation, not evidence of inclusion in a stock report.
type CustomerOrderPosition struct {
	ID, AssortmentKind, AssortmentID string
	Quantity, Reserve, Shipped       *string
	PackID, PackUnitID               *string
	PackQuantity                     *string
	ObservedAt                       time.Time
}

type CustomerOrderObservation struct {
	ID, Account                       string
	SyncID, StateID, WarehouseID      *string
	Created, Updated, Deleted, Moment *string // Original MoySklad date strings, not local observation time.
	Applicable                        *bool
	ObservedAt                        time.Time
	Positions                         []CustomerOrderPosition
}

type CustomerOrderImport struct {
	Tenant, Account                     string
	Since, Until, StartedAt, FinishedAt time.Time
	Orders                              []CustomerOrderObservation
}
