package domain

// PostingReadScope is a trusted administrative scope; it is not authentication.
type PostingReadScope struct{ Tenant, Account, MoyskladAccount string }
type PostingProductCorrelation struct {
	Status                                string
	MoyskladAccount, TargetKind, TargetID *string
	MappingVersion                        *int
}
type PostingWarehouseCorrelation struct {
	Status                       string
	MoyskladAccount, WarehouseID *string
	MappingVersion               *int
}
type PostingLineView struct {
	PostingLine
	Correlation PostingProductCorrelation
}
type PostingView struct {
	Tenant, Account        string
	BatchID, ObservationID int64
	Observation            PostingObservation
	Lines                  []PostingLineView
	Warehouse              PostingWarehouseCorrelation
}
