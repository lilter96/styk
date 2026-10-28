package moyskladfixture

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"
)

type StockResult struct {
	Status   string  `json:"status"`
	Stock    *string `json:"stock"`
	Reserve  *string `json:"reserve"`
	Incoming *string `json:"incoming"`
}

// AllStockMatches concerns physical stock only; it does not establish reserve attribution.
func AllStockMatches(results map[string]StockResult) bool {
	if len(results) != 4 && len(results) != 6 {
		return false
	}
	keys := []string{"product-a/warehouse-a", "product-a/warehouse-b", "product-b/warehouse-a", "product-c/warehouse-b"}
	if len(results) == 6 {
		keys = append(keys, "variant-one/warehouse-a", "variant-two/warehouse-a")
	}
	for _, key := range keys {
		r, ok := results[key]
		if !ok || r.Status != "STOCK_MATCH" {
			return false
		}
	}
	return true
}

// ObserveStock reads only the fixture pairs from one completed, bounded report
// traversal. A missing pair stays missing rather than being turned into zero.
func (c *Client) ObserveStock(ctx context.Context, m Manifest) (map[string]StockResult, error) {
	if err := validateManifest(m, m.Namespace); err != nil {
		return nil, err
	}
	hash, err := AccountHash(c.account)
	if err != nil || hash != m.AccountHash {
		return nil, errors.New("stock observation account does not match fixture manifest")
	}
	if _, ok := findID(m, "stock-a"); !ok {
		return nil, errors.New("first stock entry is not verified")
	}
	if _, ok := findID(m, "stock-b"); !ok {
		return nil, errors.New("second stock entry is not verified")
	}
	type pair struct {
		item, warehouse, kind string
		expected              int
	}
	targets := map[string]pair{}
	productBStock := 5
	for _, op := range m.Operations {
		if op.Key == "shipment-partial" {
			if op.State == Unknown {
				return nil, errors.New("shipment outcome unknown; stock expectation unavailable")
			}
			if op.State == Created {
				productBStock = 4
			}
		}
	}
	wants := []struct {
		label, item, warehouse, kind string
		qty                          int
	}{{"product-a/warehouse-a", "product-a", "warehouse-a", "product", 10}, {"product-b/warehouse-a", "product-b", "warehouse-a", "product", productBStock}, {"product-a/warehouse-b", "product-a", "warehouse-b", "product", 4}, {"product-c/warehouse-b", "product-c", "warehouse-b", "product", 12}}
	if m.MultipleOrderProductOnly {
		if _, ok := findID(m, "stock-variants"); !ok {
			return nil, errors.New("variant stock entry is not verified")
		}
		wants = append(wants, struct {
			label, item, warehouse, kind string
			qty                          int
		}{"variant-one/warehouse-a", "variant-one", "warehouse-a", "variant", 8}, struct {
			label, item, warehouse, kind string
			qty                          int
		}{"variant-two/warehouse-a", "variant-two", "warehouse-a", "variant", 3})
	}
	for _, x := range wants {
		item, ok := findID(m, x.item)
		if !ok {
			return nil, errors.New("stock fixture item is not verified")
		}
		warehouse, ok := findID(m, x.warehouse)
		if !ok {
			return nil, errors.New("stock fixture warehouse is not verified")
		}
		targets[x.label] = pair{item, warehouse, x.kind, x.qty}
	}
	p, err := c.page(ctx, "report/stock/bystore", url.Values{"limit": {"100"}, "offset": {"0"}, "filter": {"stockMode=all"}, "groupBy": {"variant"}})
	if err != nil {
		return nil, err
	}
	if len(*p.Rows) != *p.Meta.Size {
		return nil, errors.New("stock report exceeds bounded traversal")
	}
	out := map[string]StockResult{}
	for key := range targets {
		out[key] = StockResult{Status: "MISSING"}
	}
	seen := map[string]bool{}
	for _, item := range *p.Rows {
		meta, ok := item["meta"].(map[string]any)
		if !ok {
			return nil, errors.New("stock item metadata missing")
		}
		kind := str(meta, "type")
		if kind != "product" && kind != "variant" {
			continue
		}
		id, ok := referenceID(item, kind)
		if !ok {
			continue
		}
		stores, ok := item["stockByStore"].([]any)
		if !ok {
			return nil, errors.New("warehouse stock collection missing")
		}
		for _, raw := range stores {
			row, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("invalid warehouse stock row")
			}
			warehouse, ok := referenceID(row, "store")
			if !ok {
				return nil, errors.New("invalid warehouse reference")
			}
			for label, want := range targets {
				if id != want.item || warehouse != want.warehouse || kind != want.kind {
					continue
				}
				if seen[label] {
					return nil, errors.New("duplicate warehouse stock row")
				}
				seen[label] = true
				result := StockResult{Status: "MISSING_VALUE"}
				if v, ok := row["stock"].(json.Number); ok {
					s := v.String()
					result.Stock = &s
				}
				if v, ok := row["reserve"].(json.Number); ok {
					s := v.String()
					result.Reserve = &s
				}
				if v, ok := row["inTransit"].(json.Number); ok {
					s := v.String()
					result.Incoming = &s
				}
				if result.Stock != nil && result.Reserve != nil && result.Incoming != nil {
					if equalNumber(*result.Stock, want.expected) {
						result.Status = "STOCK_MATCH"
					} else {
						result.Status = "STOCK_MISMATCH"
					}
				}
				out[label] = result
			}
		}
	}
	return out, nil
}

// PollStock performs at most attempts observations, and never makes writes.
func (c *Client) PollStock(ctx context.Context, m Manifest, attempts int, delay time.Duration) (map[string]StockResult, error) {
	if attempts < 1 || attempts > 10 || delay < time.Second || delay > time.Minute {
		return nil, errors.New("invalid stock polling limits")
	}
	var result map[string]StockResult
	for i := 0; i < attempts; i++ {
		var err error
		result, err = c.ObserveStock(ctx, m)
		if err != nil {
			return nil, err
		}
		if AllStockMatches(result) {
			return result, nil
		}
		if i+1 == attempts {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return result, nil
}
