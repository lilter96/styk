package ozon

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/lilter96/styk/internal/delivery"
	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

var ErrOrderUnsupported = errors.New("ozon order operation is unsupported")

// OrdersClient exposes the order reader. Mutation methods live in separately
// bound adapters and cannot pass through the generic read transport.
type OrdersClient struct {
	read                  *Client
	scope                 orders.Scope
	manifest              ordercontract.Manifest
	ReadonlyReaderRequest bool
	options               OrderReadOptions
	now                   func() time.Time
	deliveryMu            sync.Mutex
	deliveryCache         *delivery.Discovery
	deliveryFlight        chan struct{}
}

// OrderReadOptions requests separately authorized optional provider groups.
// CustomerPII remains transient and must be protected before a page is stored.
type OrderReadOptions struct {
	AnalyticsData    bool              `json:"analytics_data"`
	FinancialData    bool              `json:"financial_data"`
	LegalInfo        bool              `json:"legal_info"`
	Barcodes         bool              `json:"barcodes"`
	CustomerPII      bool              `json:"-"`
	ResolveDelivery  bool              `json:"-"`
	DeliveryObserver delivery.Observer `json:"-"`
}

func NewOrderClient(client *Client, scope orders.Scope) (*OrdersClient, error) {
	return NewOrderClientWithOptions(client, scope, OrderReadOptions{AnalyticsData: true, FinancialData: true, Barcodes: true})
}

func NewOrderClientWithOptions(client *Client, scope orders.Scope, options OrderReadOptions) (*OrdersClient, error) {
	if client == nil || scope.Validate() != nil || scope.OzonAccount != client.id || (options.LegalInfo && !options.CustomerPII) {
		return nil, orders.ErrInvalid
	}
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		return nil, err
	}
	if client.attempts != 1 {
		return nil, orders.ErrInvalid
	}
	return &OrdersClient{read: client, scope: scope, manifest: manifest, ReadonlyReaderRequest: true, options: options, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (c *OrdersClient) ReadOrderCorrelation(_ context.Context, _ orders.Correlation) (orders.Correlation, error) {
	return orders.Correlation{}, ErrOrderUnsupported
}

func orderReadPath(path string) bool {
	switch path {
	case "/v4/posting/fbs/list", "/v4/posting/fbs/unfulfilled/list", "/v3/posting/fbs/get", "/v2/warehouse/list", "/v2/delivery-method/list", "/v1/posting/fbs/cancel-reason":
		return true
	default:
		return false
	}
}
