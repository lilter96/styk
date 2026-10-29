package domain

import "time"

// AutomaticStockTarget is the explicitly selected, versioned mapping and the
// operator's standing assumptions. These are not provider observations.
type AutomaticStockTarget struct {
	ProductID               string `json:"ozon_product_id"`
	WarehouseID             string `json:"ozon_warehouse_id"`
	SourceItemID            string `json:"source_item_id"`
	SourceWarehouseID       string `json:"source_warehouse_id"`
	ProductMappingID        int64  `json:"product_mapping_id"`
	ProductMappingVersion   int    `json:"product_mapping_version"`
	WarehouseMappingID      int64  `json:"warehouse_mapping_id"`
	WarehouseMappingVersion int    `json:"warehouse_mapping_version"`
	SafetyStock             string `json:"safety_stock"`
	Unreflected             string `json:"unreflected"`
	UnseenBound             string `json:"unseen_bound"`
	Maximum                 int64  `json:"maximum"`
	// A separate, explicit takeover of this target with an unknown Ozon
	// baseline. It is not evidence that the prior balance or reserve was zero.
	InitialPublication bool `json:"initial_publication,omitempty"`
}

type AutomaticStockPolicy struct {
	StockSyncScope
	Revision        int64                  `json:"revision"`
	Enabled         bool                   `json:"enabled"`
	IntervalSeconds int                    `json:"interval_seconds"`
	CreatedAt       time.Time              `json:"created_at"`
	ValidUntil      time.Time              `json:"valid_until"`
	Targets         []AutomaticStockTarget `json:"targets"`
	EvidenceClass   string                 `json:"evidence_class"`
}

type AutomaticStockRequest struct {
	StockSyncScope
	Enabled         bool                          `json:"enabled"`
	IntervalSeconds int                           `json:"interval_seconds"`
	ValidMinutes    int                           `json:"valid_minutes"`
	ProductID       string                        `json:"ozon_product_id"`
	WarehouseID     string                        `json:"ozon_warehouse_id"`
	SafetyStock     string                        `json:"safety_stock"`
	Unreflected     string                        `json:"unreflected"`
	UnseenBound     string                        `json:"unseen_bound"`
	Maximum         int64                         `json:"maximum"`
	Targets         []AutomaticStockTargetRequest `json:"targets,omitempty"`
	Acknowledge     bool                          `json:"acknowledge"`
	IdempotencyKey  string                        `json:"idempotency_key"`
	SessionID       string                        `json:"-"`
}

type AutomaticStockTargetRequest struct {
	ProductID          string `json:"ozon_product_id"`
	WarehouseID        string `json:"ozon_warehouse_id"`
	SafetyStock        string `json:"safety_stock"`
	Unreflected        string `json:"unreflected"`
	UnseenBound        string `json:"unseen_bound"`
	Maximum            int64  `json:"maximum"`
	InitialPublication bool   `json:"initial_publication,omitempty"`
}

type AutomaticStockCycle struct {
	ID                string         `json:"id"`
	ConfigRevision    int64          `json:"config_revision"`
	State             string         `json:"state"`
	Outcome           string         `json:"outcome,omitempty"`
	StartedAt         time.Time      `json:"started_at"`
	FinishedAt        *time.Time     `json:"finished_at,omitempty"`
	MoyskladImportID  string         `json:"moysklad_import_id,omitempty"`
	OzonImportID      string         `json:"ozon_import_id,omitempty"`
	PublicationID     string         `json:"publication_id,omitempty"`
	ReconcileAttempts int            `json:"reconcile_attempts"`
	Metrics           map[string]any `json:"metrics"`
}

type AutomaticStockView struct {
	Available   bool                       `json:"available"`
	Policy      *AutomaticStockPolicy      `json:"policy,omitempty"`
	NextRunAt   *time.Time                 `json:"next_run_at,omitempty"`
	LastCycleAt *time.Time                 `json:"last_cycle_at,omitempty"`
	LastCycle   *AutomaticStockCycle       `json:"last_cycle,omitempty"`
	Unresolved  []AutomaticStockUnresolved `json:"unresolved,omitempty"`
	Candidates  []AutomaticStockCandidate  `json:"candidates,omitempty"`
}

// Candidates are confirmed mappings observed in the most recent imported
// catalog. Quantities are observations, not a new calculated decision.
type AutomaticStockCandidate struct {
	ProductID        string     `json:"ozon_product_id"`
	WarehouseID      string     `json:"ozon_warehouse_id"`
	OfferID          string     `json:"offer_id"`
	SourceItemID     string     `json:"source_item_id"`
	Eligible         bool       `json:"eligible"`
	InitialEligible  bool       `json:"initial_eligible"`
	SourcePhysical   *string    `json:"source_physical,omitempty"`
	SourceReserve    *string    `json:"source_reserve,omitempty"`
	MarketplaceFree  *int64     `json:"marketplace_free,omitempty"`
	LastOperationID  *string    `json:"last_operation_id,omitempty"`
	LastState        *string    `json:"last_state,omitempty"`
	LastQuantity     *string    `json:"last_quantity,omitempty"`
	ReadbackQuantity *string    `json:"readback_quantity,omitempty"`
	ReadbackAt       *time.Time `json:"readback_at,omitempty"`
}

type AutomaticStockUnresolved struct {
	OperationID      string     `json:"operation_id"`
	ProductID        string     `json:"ozon_product_id"`
	WarehouseID      string     `json:"ozon_warehouse_id"`
	Quantity         string     `json:"quantity"`
	State            string     `json:"state"`
	SafeError        string     `json:"safe_error,omitempty"`
	DispatchedAt     *time.Time `json:"dispatched_at,omitempty"`
	ReadbackAt       *time.Time `json:"readback_at,omitempty"`
	ReadbackQuantity *string    `json:"readback_quantity,omitempty"`
}
