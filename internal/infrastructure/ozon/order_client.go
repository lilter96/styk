package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

func (c *OrdersClient) ReadOrderLabelTask(ctx context.Context, scope orders.Scope, taskID string) (orders.LabelTask, error) {
	if scope != c.scope || strings.TrimSpace(taskID) == "" {
		return orders.LabelTask{}, orders.ErrInvalid
	}
	if !c.ReadonlyReaderRequest || !orderReadPath(labelTaskPath) {
		return orders.LabelTask{}, orders.ErrBlocked
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	limits := Limits{Requests: 1, Attempts: 1, Pages: 1, Entities: 100, Products: 100, Warehouses: 100, Postings: 100, Observations: 100, ResponseBytes: 4 << 20}
	ctx = context.WithValue(ctx, budgetKey{}, &budget{limits: limits})
	body := struct {
		TaskID string `json:"task_id"`
	}{taskID}
	var result json.RawMessage
	if err := c.read.post(ctx, labelTaskPath, body, &result); err != nil {
		return orders.LabelTask{}, fmt.Errorf("read Ozon label task: %w", err)
	}
	if !c.validOrderJSON(labelTaskPath, result) {
		return orders.LabelTask{}, errOrderPayload
	}
	fields := newOrderFields(result)
	task := orders.LabelTask{ID: taskID}
	if code := fields.text("code"); code.Presence == orders.PresenceValue && code.Value != nil {
		task.Code = *code.Value
	}
	if ec := fields.text("error_code"); ec.Presence == orders.PresenceValue && ec.Value != nil {
		task.ErrorCode = *ec.Value
	}
	if em := fields.text("error_message"); em.Presence == orders.PresenceValue && em.Value != nil {
		task.ErrorMessage = *em.Value
	}
	if pc := fields.text("postings_count"); pc.Presence == orders.PresenceValue && pc.Value != nil {
		if n, e := strconv.Atoi(*pc.Value); e == nil {
			task.PostingsCount = n
		}
	}
	if ppc := fields.text("printed_postings_count"); ppc.Presence == orders.PresenceValue && ppc.Value != nil {
		if n, e := strconv.Atoi(*ppc.Value); e == nil {
			task.PrintedPostingsCount = n
		}
	}
	if task.Code == "" {
		task.Code = "pending"
	}
	return task, nil
}

func orderReadPath(path string) bool {
	switch path {
	case "/v4/posting/fbs/list", "/v4/posting/fbs/unfulfilled/list", "/v3/posting/fbs/get", "/v2/warehouse/list", "/v2/delivery-method/list", "/v1/posting/fbs/cancel-reason", labelTaskPath:
		return true
	default:
		return false
	}
}

const labelTaskPath = "/v3/posting/fbs/package-label/task"
