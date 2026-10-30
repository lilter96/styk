package ozon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

const (
	orderListPath        = "/v4/posting/fbs/list"
	orderUnfulfilledPath = "/v4/posting/fbs/unfulfilled/list"
	orderGetPath         = "/v3/posting/fbs/get"
)

var _ orders.Reader = (*OrdersClient)(nil)
var _ orders.SourceReader = (*OrdersClient)(nil)
var _ orders.LabelTaskReader = (*OrdersClient)(nil)

func (c *OrdersClient) orderPost(ctx context.Context, path string, body any) (json.RawMessage, error) {
	if !c.ReadonlyReaderRequest || !orderReadPath(path) {
		return nil, orders.ErrBlocked
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	limits := Limits{Requests: 1, Attempts: 1, Pages: 1, Entities: 100, Products: 100, Warehouses: 100, Postings: 100, Observations: 100, ResponseBytes: 4 << 20}
	ctx = context.WithValue(ctx, budgetKey{}, &budget{limits: limits})
	var result json.RawMessage
	if err := c.read.post(ctx, path, body, &result); err != nil {
		return nil, fmt.Errorf("read Ozon order: %w", err)
	}
	return result, nil
}

func (c *OrdersClient) ReadOrderPosting(ctx context.Context, scope orders.Scope, number string) (orders.Posting, error) {
	if scope != c.scope || strings.TrimSpace(number) == "" || len(number) > 200 || strings.ContainsAny(number, "\r\n") {
		return orders.Posting{}, orders.ErrInvalid
	}
	body := struct {
		PostingNumber string           `json:"posting_number"`
		With          OrderReadOptions `json:"with"`
	}{number, c.options}
	data, err := c.orderPost(ctx, orderGetPath, body)
	if err != nil {
		return orders.Posting{}, err
	}
	if !c.validOrderJSON(orderGetPath, data) {
		return orders.Posting{}, errOrderPayload
	}
	fields := newOrderFields(data)
	posting, err := c.decodeOrderPosting(fields.object("result"), c.now())
	if err != nil || posting.PostingNumber != number {
		return orders.Posting{}, errOrderPayload
	}
	posting.Drift = c.orderDrift(orderGetPath, data, posting.Drift)
	if c.options.ResolveDelivery {
		posting, err = c.EnrichDelivery(ctx, scope, posting)
		if err != nil {
			return orders.Posting{}, err
		}
	}
	return finishOrderPosting(posting)
}

func (c *OrdersClient) ReadOrderPage(ctx context.Context, scope orders.Scope, window orders.OrderWindow, cursor string) (orders.OrderSourcePage, error) {
	path, err := c.orderPagePath(scope, window, cursor)
	if err != nil {
		return orders.OrderSourcePage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	request := c.orderPageRequest(path, window, cursor)
	data, err := c.orderPost(ctx, path, request)
	if err != nil {
		return orders.OrderSourcePage{}, err
	}
	if !c.validOrderJSON(path, data) {
		return orders.OrderSourcePage{}, errOrderPayload
	}
	return c.readOrderPageDetails(ctx, data, cursor, window.Limit)
}

func (c *OrdersClient) orderPagePath(scope orders.Scope, window orders.OrderWindow, cursor string) (string, error) {
	if scope != c.scope || window.From.IsZero() || !window.To.After(window.From) || window.To.Sub(window.From) > 31*24*time.Hour || window.Limit < 1 || window.Limit > 100 || len(cursor) > 4096 {
		return "", orders.ErrInvalid
	}
	switch window.Stream {
	case "ozon-orders-discovery":
		return orderListPath, nil
	case "ozon-orders-unfulfilled":
		return orderUnfulfilledPath, nil
	default:
		return "", orders.ErrInvalid
	}
}

func (c *OrdersClient) orderPageRequest(path string, window orders.OrderWindow, cursor string) any {
	filter := map[string]string{"since": window.From.UTC().Format(time.RFC3339Nano), "to": window.To.UTC().Format(time.RFC3339Nano)}
	if path == orderUnfulfilledPath {
		filter = map[string]string{"cutoff_from": window.From.UTC().Format(time.RFC3339Nano), "cutoff_to": window.To.UTC().Format(time.RFC3339Nano)}
	}
	return struct {
		Cursor  string            `json:"cursor"`
		Filter  map[string]string `json:"filter"`
		Limit   int               `json:"limit"`
		SortDir string            `json:"sort_dir"`
		With    OrderReadOptions  `json:"with"`
	}{cursor, filter, window.Limit, "asc", c.options}
}

func (c *OrdersClient) readOrderPageDetails(ctx context.Context, data []byte, cursor string, limit int) (orders.OrderSourcePage, error) {
	fields := newOrderFields(data)
	rows := fields.array("postings")
	more, next := fields.boolean("has_next"), fields.text("cursor")
	if *fields.err != nil || rows == nil || !more.IsKnown() || len(rows) > limit {
		return orders.OrderSourcePage{}, errOrderPayload
	}
	page := orders.OrderSourcePage{Postings: make([]orders.Posting, 0, len(rows)), Complete: !*more.Value}
	if !page.Complete && (!next.IsKnown() || *next.Value == cursor || len(rows) == 0) {
		return orders.OrderSourcePage{}, errOrderPayload
	}
	if !page.Complete {
		page.NextCursor = *next.Value
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		number := orderKnown(row.text("posting_number"))
		if *fields.err != nil || number == "" || seen[number] {
			return orders.OrderSourcePage{}, errOrderPayload
		}
		seen[number] = true
		posting, err := c.ReadOrderPosting(ctx, c.scope, number)
		if err != nil {
			return orders.OrderSourcePage{}, err
		}
		page.Postings = append(page.Postings, posting)
	}
	return page, nil
}

func (c *OrdersClient) validOrderJSON(path string, data []byte) bool {
	for _, issue := range c.manifest.Validate(path, "response", data).Issues {
		if issue.Code == "invalid_json" || issue.Code == "invalid_manifest" || issue.Code == "unknown_contract" {
			return false
		}
	}
	return true
}

// ReadActive refreshes exact persisted keys, including postings created outside
// the discovery window. It never scans the product catalog.
func (c *OrdersClient) ReadActive(ctx context.Context, scope orders.Scope, numbers []string) ([]orders.Posting, error) {
	if scope != c.scope || len(numbers) > 100 {
		return nil, orders.ErrInvalid
	}
	result := make([]orders.Posting, 0, len(numbers))
	seen := make(map[string]bool, len(numbers))
	for _, number := range numbers {
		if seen[number] {
			return nil, orders.ErrInvalid
		}
		seen[number] = true
		posting, err := c.ReadOrderPosting(ctx, scope, number)
		if err != nil {
			return nil, err
		}
		result = append(result, posting)
	}
	return result, nil
}
