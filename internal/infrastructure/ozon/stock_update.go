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
	"strings"
)

const stockUpdatePath = "/v2/products/stocks"

var ErrStockWriteUnknown = errors.New("Ozon stock write outcome is unknown")
var ErrStockReadbackUnavailable = errors.New("Ozon stock readback is unavailable")

type StockUpdate struct {
	ProductID, WarehouseID string
	Quantity               int64
}
type StockUpdateResult struct{ ProductID, WarehouseID, State, SafeError string }

func safeStockCode(v string) string {
	switch strings.ToUpper(strings.ReplaceAll(v, " ", "_")) {
	case "SKU_STOCK_NOT_CHANGED", "PRODUCT_HAS_NOT_BEEN_TAGGED_YET", "NON_KGT_ON_KGT_WAREHOUSE", "PRICE_IS_NOT_SENT", "TOO_MANY_REQUESTS", "PRODUCT_IS_NOT_CREATED", "NOT_PASS_MODERATION", "INVALID_ARGUMENT", "PERMISSION_DENIED", "UNAUTHENTICATED", "VALIDATION_ERROR", "INTERNAL_ERROR":
		return strings.ToUpper(strings.ReplaceAll(v, " ", "_"))
	default:
		return "MARKETPLACE_REJECTED"
	}
}

// UpdateStocks sends exactly ONE HTTP attempt. Read-client retries are never
// reused for an external write. Any ambiguous transport/schema/server result
// must be reconciled by an explicit read; it is not a retry instruction.
func (c *Client) UpdateStocks(ctx context.Context, items []StockUpdate) ([]StockUpdateResult, error) {
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
		return nil, ErrStockWriteUnknown
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, ErrStockWriteUnknown
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 || resp.StatusCode == 409 || resp.StatusCode == 429 {
			out := make([]StockUpdateResult, 0, len(items))
			for _, v := range items {
				out = append(out, StockUpdateResult{v.ProductID, v.WarehouseID, "FAILED", "HTTP_" + strconv.Itoa(resp.StatusCode)})
			}
			return out, nil
		}
		return nil, ErrStockWriteUnknown
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
		return nil, ErrStockWriteUnknown
	}
	out := make([]StockUpdateResult, 0, len(items))
	returned := map[string]StockUpdateResult{}
	for _, v := range *decoded.Result {
		p, w := id(v.ProductID), id(v.WarehouseID)
		key := p + ":" + w
		if !seen[key] || v.Updated == nil || v.Errors == nil {
			continue
		}
		if _, duplicate := returned[key]; duplicate {
			return nil, ErrStockWriteUnknown
		}
		r := StockUpdateResult{ProductID: p, WarehouseID: w, State: "UNKNOWN", SafeError: "INCOMPLETE_RESPONSE"}
		if *v.Updated && len(*v.Errors) == 0 {
			r.State, r.SafeError = "SUCCEEDED", ""
		} else if !*v.Updated && len(*v.Errors) > 0 {
			r.State, r.SafeError = "FAILED", safeStockCode((*v.Errors)[0].Code)
		}
		returned[key] = r
	}
	for _, v := range items {
		r, ok := returned[v.ProductID+":"+v.WarehouseID]
		if !ok {
			r = StockUpdateResult{v.ProductID, v.WarehouseID, "UNKNOWN", "TARGET_RESULT_MISSING"}
		}
		out = append(out, r)
	}
	return out, nil
}

// ReadStockQuantity is a bounded, read-only observation. Matching free stock
// establishes the current effect, never that an earlier request executed.
func (c *Client) ReadStockQuantity(ctx context.Context, productID, warehouseID, offerID string) (int64, error) {
	if productID == "" || warehouseID == "" || offerID == "" {
		return 0, ErrStockReadbackUnavailable
	}
	var response stockResponse
	if err := c.post(ctx, stockPath, map[string]any{"offer_id": []string{offerID}, "limit": 100, "cursor": ""}, &response); err != nil {
		return 0, ErrStockReadbackUnavailable
	}
	if response.Products == nil {
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
