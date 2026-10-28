package domain

import "time"

type WarehouseIdentity struct{ System, Account, ID string }
type WarehouseMapping struct {
	Tenant              string
	Source, Destination WarehouseIdentity
	Status, Provenance  string
	Version             int
	ConfirmedAt         time.Time
}
