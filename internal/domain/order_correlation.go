package domain

type OrderCorrelationStatus string

const (
	OrderIdentityConfirmed OrderCorrelationStatus = "CONFIRMED_BY_EXPLICIT_IDENTITY"
	OrderCandidateOnly     OrderCorrelationStatus = "CANDIDATE_ONLY"
	OrderAmbiguous         OrderCorrelationStatus = "AMBIGUOUS"
	OrderConflict          OrderCorrelationStatus = "CONFLICT"
	OrderNotFound          OrderCorrelationStatus = "NOT_FOUND"
	OrderInsufficient      OrderCorrelationStatus = "INSUFFICIENT_EVIDENCE"
)

type OrderCorrelationCandidate struct {
	OrderObservationID           int64
	OrderID                      string
	WarehouseID                  *string
	SourceUpdated, SourceDeleted *string
	Applicable                   *bool
	HistoryConflict              bool
	Positions                    []OrderPositionEvidence
	WarehouseMappingID           int64
	WarehouseMappingVersion      int
}

type OrderPositionEvidence struct {
	PositionID, AssortmentKind, AssortmentID string
	Quantity, Reserve, Shipped               *string
	ObservedAt                               string
	ProductMappingID                         int64
	ProductMappingVersion                    int
}

type OrderCorrelation struct {
	Tenant, MoyskladAccount, OzonAccount       string
	PostingObservationID, CustomerOrderBatchID int64
	PostingNumber                              string
	Status                                     OrderCorrelationStatus
	Candidates                                 []OrderCorrelationCandidate
	Reasons                                    []string
	Truncated                                  bool
}
