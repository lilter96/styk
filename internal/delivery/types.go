// Package delivery records explicit, scoped warehouse and delivery facts.
// Discovery facts do not authorize stock publication or posting transitions.
package delivery

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

const Validity = 5 * time.Minute

type Scope struct {
	Tenant      string `json:"tenant"`
	OzonAccount string `json:"ozon_account"`
}

type ContractEvidence struct {
	Hash             string    `json:"hash"`
	CheckedAt        time.Time `json:"checked_at"`
	SchemaVerified   bool      `json:"schema_verified"`
	VersionsVerified bool      `json:"versions_verified"`
}

// Warehouse and Method preserve source presence and separate identity spaces.
type Warehouse struct {
	ID      string               `json:"id"`
	Type    orders.Field[string] `json:"type"`
	Status  orders.Field[string] `json:"status"`
	IsRFBS  orders.Field[bool]   `json:"is_rfbs"`
	Express orders.Field[bool]   `json:"express"`
}

type Method struct {
	ID          string             `json:"id"`
	WarehouseID string             `json:"warehouse_id"`
	Express     orders.Field[bool] `json:"express"`
}

type Profile struct {
	Scope              Scope                `json:"scope"`
	ID                 string               `json:"id"`
	Revision           int64                `json:"revision"`
	WarehouseID        string               `json:"warehouse_id"`
	MethodID           orders.Field[string] `json:"method_id"`
	MethodVerified     bool                 `json:"method_verified"`
	Scheme             orders.Field[string] `json:"scheme"`
	WarehouseType      orders.Field[string] `json:"warehouse_type"`
	WarehouseStatus    orders.Field[string] `json:"warehouse_status"`
	Express            orders.Field[bool]   `json:"express"`
	CarrierIntegration orders.Field[string] `json:"carrier_integration"`
	DestinationType    orders.Field[string] `json:"destination_type"`
	VerifiedDimensions []string             `json:"verified_dimensions"`
	Contract           ContractEvidence     `json:"contract"`
	ObservedAt         time.Time            `json:"observed_at"`
	CheckedAt          time.Time            `json:"checked_at"`
	ValidUntil         time.Time            `json:"valid_until"`
	Drift              []orders.Drift       `json:"drift"`
}

type Discovery struct {
	Scope      Scope       `json:"scope"`
	Warehouses []Warehouse `json:"warehouses"`
	Methods    []Method    `json:"methods"`
	Profiles   []Profile   `json:"profiles"`
	Complete   bool        `json:"complete"`
	CheckedAt  time.Time   `json:"checked_at"`
	ValidUntil time.Time   `json:"valid_until"`
}

type Observer interface {
	SaveDeliveryObservations(context.Context, Scope, []Profile) error
}

func OrderScope(scope orders.Scope) Scope {
	return Scope{Tenant: scope.Tenant, OzonAccount: scope.OzonAccount}
}
