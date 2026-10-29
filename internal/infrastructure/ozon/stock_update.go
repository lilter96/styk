package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lilter96/styk/internal/testpublication"
)

const stockUpdatePath = "/v2/products/stocks"

var ErrStockWritesDisabled = errors.New("stock writes are disabled")

var ErrStockWriteUnknown = errors.New("Ozon stock write outcome is unknown")
var ErrStockReadbackUnavailable = errors.New("Ozon stock readback is unavailable")

type StockUpdate struct {
	ProductID, WarehouseID string
	Quantity               int64
}
type StockUpdateResult struct {
	ProductID, WarehouseID, State, SafeError string
	HTTPStatus                               int
	Updated                                  *bool
	ErrorCodes                               []string
}

// StockWriteError preserves a safe HTTP status without treating any error
// response as proof that the external mutation did not happen.
type StockWriteError struct{ HTTPStatus int }

func (e *StockWriteError) Error() string { return ErrStockWriteUnknown.Error() }
func (e *StockWriteError) Unwrap() error { return ErrStockWriteUnknown }

func safeStockCode(v string) string {
	// The provider response is untrusted. Preserve known protocol codes, but
	// never persist arbitrary strings that could contain credentials or data.
	switch v {
	case "SKU_STOCK_NOT_CHANGED", "PRODUCT_HAS_NOT_BEEN_TAGGED_YET", "NON_KGT_ON_KGT_WAREHOUSE", "PRICE_IS_NOT_SENT", "TOO_MANY_REQUESTS", "PRODUCT_IS_NOT_CREATED", "NOT_PASS_MODERATION", "INVALID_ARGUMENT", "PERMISSION_DENIED", "UNAUTHENTICATED", "VALIDATION_ERROR", "INTERNAL_ERROR":
		return v
	default:
		return "MARKETPLACE_REJECTED"
	}
}

// BindControlledTestIntent restricts this single-use client to one frozen
// publication. The worker supplies an authorization callback that rechecks the
// claimed outbox task and current evidence in Postgres before dispatch.
func (c *Client) BindControlledTestIntent(item StockUpdate, authorize func(context.Context) (bool, error)) error {
	c.intentMu.Lock()
	defer c.intentMu.Unlock()
	if c.controlledTestScope == nil && c.allowSyntheticStockWrites {
		// Deterministic loopback fixtures already have a separate hard host
		// boundary; the worker still performs its database checks.
		return nil
	}
	if c.controlledTestScope == nil || !testpublication.Enabled(*c.controlledTestScope) || c.controlledIntent != nil || authorize == nil || item.ProductID == "" || item.WarehouseID == "" || !testpublication.AutomaticQuantityAllowed(strconv.FormatInt(item.Quantity, 10)) {
		return ErrStockWritesDisabled
	}
	c.controlledIntent = &item
	c.controlledAuthorize = authorize
	return nil
}

func (c *Client) consumeStockIntent(items []StockUpdate) (func(context.Context) (bool, error), error) {
	if c.controlledTestScope == nil {
		if c.allowSyntheticStockWrites {
			return nil, nil
		}
		return nil, ErrStockWritesDisabled
	}
	if !testpublication.Enabled(*c.controlledTestScope) || len(items) != 1 {
		return nil, ErrStockWritesDisabled
	}
	c.intentMu.Lock()
	defer c.intentMu.Unlock()
	if c.controlledIntent == nil || c.controlledIntentUsed || *c.controlledIntent != items[0] || c.controlledAuthorize == nil || !testpublication.AutomaticQuantityAllowed(strconv.FormatInt(items[0].Quantity, 10)) {
		return nil, ErrStockWritesDisabled
	}
	c.controlledIntentUsed = true
	return c.controlledAuthorize, nil
}

func (c *Client) authorizeStockIntent(ctx context.Context, authorize func(context.Context) (bool, error)) error {
	if authorize == nil {
		return nil
	}
	if !testpublication.Enabled(*c.controlledTestScope) {
		return ErrStockWritesDisabled
	}
	allowed, err := authorize(ctx)
	if err != nil || !allowed || !testpublication.Enabled(*c.controlledTestScope) {
		return ErrStockWritesDisabled
	}
	return ctx.Err()
}

// UpdateStocks sends exactly ONE HTTP attempt. Read-client retries are never
// reused for an external write. Any ambiguous transport/schema/server result
// must be reconciled by an explicit read; it is not a retry instruction.
func (c *Client) UpdateStocks(ctx context.Context, items []StockUpdate) ([]StockUpdateResult, error) {
	authorize, err := c.consumeStockIntent(items)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 || len(items) > 100 {
		return nil, errors.New("stock update supports one to one hundred targets")
	}
	type entry struct {
		ProductID   int64 `json:"product_id"`
		WarehouseID int64 `json:"warehouse_id"`
		Stock       int64 `json:"stock"`
	}
	body := struct {
		Stocks []entry `json:"stocks"`
	}{Stocks: []entry{}}
	seen := map[string]bool{}
	for _, item := range items {
		p, pe := strconv.ParseInt(item.ProductID, 10, 64)
		w, we := strconv.ParseInt(item.WarehouseID, 10, 64)
		key := item.ProductID + ":" + item.WarehouseID
		if pe != nil || we != nil || p <= 0 || w <= 0 || strconv.FormatInt(p, 10) != item.ProductID || strconv.FormatInt(w, 10) != item.WarehouseID || item.Quantity < 0 || item.Quantity > 2147483647 || seen[key] {
			return nil, errors.New("invalid stock target")
		}
		seen[key] = true
		body.Stocks = append(body.Stocks, entry{p, w, item.Quantity})
	}
	payload, _ := json.Marshal(body)
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	if err := c.authorizeStockIntent(ctx, authorize); err != nil {
		return nil, err
	}
	u := c.base.ResolveReference(&url.URL{Path: stockUpdatePath})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("construct stock update")
	}
	req.Header.Set("Client-Id", c.id)
	req.Header.Set("Api-Key", c.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &StockWriteError{}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, &StockWriteError{HTTPStatus: resp.StatusCode}
	}
	// No available provider guarantee proves that any non-2xx response means
	// no target was applied, especially through gateways or after partial work.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &StockWriteError{HTTPStatus: resp.StatusCode}
	}
	var decoded struct {
		Result *[]struct {
			ProductID   *integer `json:"product_id"`
			WarehouseID *integer `json:"warehouse_id"`
			Updated     *bool    `json:"updated"`
			Errors      *[]struct {
				Code string `json:"code"`
			} `json:"errors"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &decoded) != nil || decoded.Result == nil {
		return nil, &StockWriteError{HTTPStatus: resp.StatusCode}
	}
	out := make([]StockUpdateResult, 0, len(items))
	returned := map[string]StockUpdateResult{}
	for _, v := range *decoded.Result {
		p, w := id(v.ProductID), id(v.WarehouseID)
		key := p + ":" + w
		if !seen[key] || v.Updated == nil || v.Errors == nil || len(*v.Errors) > 32 {
			return nil, &StockWriteError{HTTPStatus: resp.StatusCode}
		}
		if _, duplicate := returned[key]; duplicate {
			return nil, &StockWriteError{HTTPStatus: resp.StatusCode}
		}
		r := StockUpdateResult{ProductID: p, WarehouseID: w, State: "UNKNOWN", SafeError: "INCOMPLETE_RESPONSE", HTTPStatus: resp.StatusCode, Updated: v.Updated, ErrorCodes: []string{}}
		for _, problem := range *v.Errors {
			r.ErrorCodes = append(r.ErrorCodes, safeStockCode(problem.Code))
		}
		if *v.Updated && len(*v.Errors) == 0 {
			r.State, r.SafeError = "SUCCEEDED", ""
		} else if !*v.Updated && len(*v.Errors) > 0 {
			// Without a verified provider guarantee that an error excludes a
			// delayed effect, retain the target fence and require reconciliation.
			r.State, r.SafeError = "UNKNOWN", r.ErrorCodes[0]
		}
		returned[key] = r
	}
	for _, v := range items {
		r, ok := returned[v.ProductID+":"+v.WarehouseID]
		if !ok {
			r = StockUpdateResult{ProductID: v.ProductID, WarehouseID: v.WarehouseID, State: "UNKNOWN", SafeError: "TARGET_RESULT_MISSING", HTTPStatus: resp.StatusCode}
		}
		out = append(out, r)
	}
	return out, nil
}

// ReadStockQuantity is a bounded, read-only observation. Matching free stock
// is a provider-reported value, not proof of freshness or causality.
func (c *Client) ReadStockQuantity(ctx context.Context, productID, warehouseID, offerID string) (int64, error) {
	if productID == "" || warehouseID == "" || offerID == "" {
		return 0, ErrStockReadbackUnavailable
	}
	var response stockResponse
	if err := c.post(ctx, stockPath, map[string]any{"offer_id": []string{offerID}, "limit": 100, "cursor": ""}, &response); err != nil {
		return 0, ErrStockReadbackUnavailable
	}
	// A single matching row is not conclusive when other pages may contain a
	// duplicate or conflicting target. Do not settle on an incomplete page.
	if response.Products == nil || response.HasNext == nil || *response.HasNext {
		return 0, ErrStockReadbackUnavailable
	}
	found := false
	var value int64
	for _, v := range *response.Products {
		if id(v.ProductID) == productID && id(v.WarehouseID) == warehouseID {
			if found || v.FreeStock == nil || v.OfferID != offerID {
				return 0, ErrStockReadbackUnavailable
			}
			found = true
			value = int64(*v.FreeStock)
		}
	}
	if !found || value < 0 {
		return 0, ErrStockReadbackUnavailable
	}
	return value, nil
}
