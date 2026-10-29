package domain

import "time"

const OzonCatalogImport = "OZON_CATALOG_IMPORT"
const OzonCatalogTask = "ozon_catalog_import"

// ImportLimits are chosen server-side and frozen for each operation.
type ImportLimits struct {
	Requests          int   `json:"requests"`
	Attempts          int   `json:"attempts"`
	Pages             int   `json:"pages"`
	Products          int   `json:"products"`
	Warehouses        int   `json:"warehouses"`
	Observations      int   `json:"observations"`
	ResponseBytes     int64 `json:"response_bytes"`
	DurationNS        int64 `json:"duration_ns"`
	DatabaseTimeoutNS int64 `json:"database_timeout_ns"`
}

func DefaultOzonImportLimits() ImportLimits {
	return ImportLimits{Requests: 100, Attempts: 100, Pages: 100, Products: 1000, Warehouses: 1000, Observations: 10000, ResponseBytes: 4 << 20, DurationNS: int64(3 * time.Minute), DatabaseTimeoutNS: int64(10 * time.Second)}
}

// Valid caps persisted settings even if a privileged actor modified the database.
func (l ImportLimits) Valid() bool {
	return l.Requests > 0 && l.Requests <= 100 && l.Attempts > 0 && l.Attempts <= 100 && l.Pages > 0 && l.Pages <= 100 && l.Products > 0 && l.Products <= 1000 && l.Warehouses > 0 && l.Warehouses <= 1000 && l.Observations > 0 && l.Observations <= 10000 && l.ResponseBytes > 0 && l.ResponseBytes <= 4<<20 && l.DurationNS > 0 && l.DurationNS <= int64(3*time.Minute) && l.DatabaseTimeoutNS > 0 && l.DatabaseTimeoutNS <= int64(10*time.Second)
}

type ImportCounts struct {
	Requests          int `json:"requests"`
	Attempts          int `json:"attempts"`
	Pages             int `json:"pages"`
	CompletedRequests int `json:"completed_requests"`
	CompletedPages    int `json:"completed_pages"`
	Products          int `json:"products"`
	Warehouses        int `json:"warehouses"`
	Observations      int `json:"observations"`
}
type ImportOperation struct {
	ID               string       `json:"id"`
	Tenant           string       `json:"tenant"`
	Provider         string       `json:"provider"`
	Account          string       `json:"account"`
	Type             string       `json:"operation_type"`
	State            string       `json:"state"`
	Phase            string       `json:"phase"`
	Outcome          *string      `json:"outcome"`
	LimitingResource *string      `json:"limiting_resource"`
	CreatedAt        time.Time    `json:"created_at"`
	StartedAt        *time.Time   `json:"started_at"`
	AttemptStartedAt *time.Time   `json:"attempt_started_at"`
	ProgressAt       *time.Time   `json:"progress_at"`
	LeaseExpiresAt   *time.Time   `json:"lease_expires_at"`
	FinishedAt       *time.Time   `json:"finished_at"`
	Attempt          int          `json:"attempt"`
	MaxAttempts      int          `json:"max_attempts"`
	Limits           ImportLimits `json:"limits"`
	Counts           ImportCounts `json:"counts"`
	BatchID          *int64       `json:"batch_id"`
	BatchCreated     *bool        `json:"batch_created"`
	ElapsedMS        int64        `json:"elapsed_ms"`
}
