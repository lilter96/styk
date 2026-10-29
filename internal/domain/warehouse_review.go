package domain

import (
	"errors"
	"strings"
	"time"
)

// WarehouseReview records a local proposal, not proof of physical identity.
type WarehouseReview struct {
	ID                     string    `json:"id"`
	Tenant                 string    `json:"tenant"`
	MoyskladAccount        string    `json:"moysklad_account"`
	OzonAccount            string    `json:"ozon_account"`
	MoyskladWarehouseID    string    `json:"moysklad_warehouse_id"`
	OzonWarehouseID        string    `json:"ozon_warehouse_id"`
	MoyskladImportID       int64     `json:"moysklad_import_id"`
	OzonImportID           int64     `json:"ozon_import_id"`
	ExistingMappingID      *int64    `json:"existing_mapping_id"`
	ExistingMappingVersion *int      `json:"existing_mapping_version"`
	CreatedAt              time.Time `json:"created_at"`
	ExpiresAt              time.Time `json:"expires_at"`
}
type WarehouseReviewRequest struct {
	Tenant, MoyskladAccount, OzonAccount, MoyskladWarehouseID, OzonWarehouseID, SessionID string
	Lifetime                                                                              time.Duration
}

func (r WarehouseReviewRequest) Validate() error {
	for _, value := range []string{r.Tenant, r.MoyskladAccount, r.OzonAccount, r.MoyskladWarehouseID, r.OzonWarehouseID, r.SessionID} {
		if value == "" || len(value) > 256 || value != strings.TrimSpace(value) {
			return errors.New("invalid warehouse review identity")
		}
	}
	if r.Lifetime <= 0 || r.Lifetime > 10*time.Minute {
		return errors.New("invalid warehouse review lifetime")
	}
	return nil
}

type WarehouseReviewResult struct {
	Review        WarehouseReview `json:"review"`
	Status        string          `json:"status"`
	PrincipalType string          `json:"principal_type"`
}
