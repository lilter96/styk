package orders

import (
	"context"
	"time"
)

// OrderWindow is a traversal bound, never a stream of every provider update.
type OrderWindow struct {
	Stream   string
	From, To time.Time
	Limit    int
}
type OrderSourcePage struct {
	NativeReads []NativeOrderPointRead
	Postings    []Posting
	NextCursor  string
	Complete    bool
	Exhausted   bool
	Failures    []ReadFailure
}
type SourceReader interface {
	ReadOrderPage(context.Context, Scope, OrderWindow, string) (OrderSourcePage, error)
}

// Financial facts remain separate: a seller price, buyer discount, commission
// and payout are different amounts and never imply a payment transaction.
type FinancialLine struct {
	Identity            LineIdentity   `json:"identity"`
	ProviderProductID   Field[string]  `json:"provider_product_id"`
	Price               Field[Money]   `json:"price"`
	PriceAmount         Field[Decimal] `json:"price_amount"`
	OldPrice            Field[Money]   `json:"old_price"`
	SellerDiscount      Field[Money]   `json:"seller_discount"`
	MarketplaceDiscount Field[Money]   `json:"marketplace_discount"`
	Commission          Field[Money]   `json:"commission"`
	Payout              Field[Money]   `json:"payout"`
	VAT                 Field[Decimal] `json:"vat"`
}
type FinancialFacts struct {
	Presence            Presence        `json:"presence"`
	Currency            Field[string]   `json:"currency"`
	OrderTotal          Field[Money]    `json:"order_total"`
	OrderTotalAmount    Field[Decimal]  `json:"order_total_amount"`
	SellerDiscount      Field[Money]    `json:"seller_discount"`
	MarketplaceDiscount Field[Money]    `json:"marketplace_discount"`
	Commission          Field[Money]    `json:"commission"`
	Payout              Field[Money]    `json:"payout"`
	Lines               []FinancialLine `json:"lines"`
}
type AnalyticsFacts struct {
	Presence     Presence      `json:"presence"`
	Region       Field[string] `json:"region"`
	City         Field[string] `json:"city"`
	DeliveryType Field[string] `json:"delivery_type"`
	Premium      Field[bool]   `json:"premium"`
}
type DeliveryFacts struct {
	Presence        Presence         `json:"presence"`
	IntegrationType Field[string]    `json:"integration_type"`
	Express         Field[bool]      `json:"express"`
	TPLProviderID   Field[string]    `json:"tpl_provider_id"`
	ProviderID      Field[string]    `json:"provider_id"`
	ProviderName    Field[string]    `json:"provider_name"`
	TrackingNumber  Field[string]    `json:"tracking_number"`
	DestinationType Field[string]    `json:"destination_type"`
	PickupPointID   Field[string]    `json:"pickup_point_id"`
	WindowFrom      Field[time.Time] `json:"window_from"`
	WindowTo        Field[time.Time] `json:"window_to"`
}

type TargetPosition struct {
	CompositionSafe Field[bool]    `json:"composition_safe,omitzero"`
	VATEnabled      Field[bool]    `json:"vat_enabled"`
	Shipped         Field[Decimal] `json:"shipped"`
	Discount        Field[Decimal] `json:"discount"`
	ID              string         `json:"id"`
	Identity        LineIdentity   `json:"identity"`
	MoyskladKind    string         `json:"moysklad_kind"`
	MoyskladID      string         `json:"moysklad_id"`
	Quantity        Decimal        `json:"quantity"`
	Reserve         Field[Decimal] `json:"reserve"`
	Price           Field[Money]   `json:"price"`
	VAT             Field[Decimal] `json:"vat"`
	Managed         bool           `json:"managed"`
}

// TargetObservation contains sanitized native facts, not raw customerorder JSON.
// Complete requires every position page, not a truncated embedded collection.
type TargetObservation struct {
	SourceDeleted     Field[string] `json:"source_deleted,omitzero"`
	ProviderUpdatedAt Field[string] `json:"provider_updated_at,omitzero"`
	RedactedFields    []string      `json:"redacted_fields,omitempty"`
	Scope             Scope         `json:"scope"`
	Correlation       Correlation   `json:"correlation"`
	Revision          string        `json:"revision"`
	// HeaderRevision commits to typed header facts before privacy redaction.
	// It is not a provider version, watermark or proof of snapshot atomicity.
	HeaderRevision string             `json:"header_revision,omitempty"`
	Fields         CustomerOrderWrite `json:"fields"`
	Positions      []TargetPosition   `json:"positions"`
	Sum            Field[Money]       `json:"sum"`
	ObservedAt     time.Time          `json:"observed_at"`
	Complete       bool               `json:"complete"`
}
type TargetReader interface {
	ReadCustomerOrder(context.Context, Correlation) (TargetObservation, error)
}
