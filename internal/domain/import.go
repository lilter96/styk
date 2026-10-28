package domain

import "time"

// ExternalEntity represents a source identity, not a matched internal product.
type ExternalEntity struct {
	Kind, ID, Name, Code, Article, ParentID, SourceUpdated string
}

// StockObservation preserves source numbers as decimal strings; an absent field is nil.
type StockObservation struct {
	Kind, ExternalID, SourceHref                   string
	Stock, Reserve, InTransit, Quantity, FreeStock *string
}
type InventoryImport struct {
	Account               string
	StartedAt, FinishedAt time.Time
	Entities              []ExternalEntity
	Stock                 []StockObservation
}
