package domain

import "time"

// OzonProduct keeps identifiers in their original namespaces.
type OzonProduct struct {
	ProductID, OfferID, SKU string
	Archived                *bool
	Name                    string `json:",omitempty"`
}
type OzonWarehouse struct {
	ID, Name, Status, WarehouseType string
	IsRFBS                          *bool
}
type OzonStock struct {
	ProductID, OfferID, SKU, WarehouseID, WarehouseName string
	Present, Reserved, FreeStock                        int64
}
type OzonImport struct {
	Account               string
	StartedAt, FinishedAt time.Time
	Products              []OzonProduct
	Warehouses            []OzonWarehouse
	Stocks                []OzonStock
}
