package ozon

import (
	"context"
	"errors"
	"strings"

	"github.com/lilter96/styk/internal/domain"
)

const productInfoPath = "/v3/product/info/list"
const productInfoBatch = 1000

type productInfo struct {
	ID      *integer `json:"id"`
	OfferID string   `json:"offer_id"`
	Name    string   `json:"name"`
}

// Names are display metadata only. They cannot establish a product mapping or
// replace identifiers from the catalog traversal. Enrichment uses the same
// bounded, authenticated, read-only transport and request accounting.
func (c *Client) readProductNames(ctx context.Context, products []domain.OzonProduct) error {
	for start := 0; start < len(products); start += productInfoBatch {
		end := min(start+productInfoBatch, len(products))
		if err := c.readProductNameBatch(ctx, products[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) readProductNameBatch(ctx context.Context, products []domain.OzonProduct) error {
	ids := make([]string, 0, len(products))
	wanted := make(map[string]int, len(products))
	for i, p := range products {
		ids = append(ids, p.ProductID)
		wanted[p.ProductID] = i
	}
	var response struct {
		Items *[]productInfo `json:"items"`
	}
	if err := c.post(ctx, productInfoPath, map[string]any{"product_id": ids}, &response); err != nil {
		return err
	}
	if response.Items == nil || len(*response.Items) != len(products) {
		return errors.New("incomplete product information")
	}
	seen := make(map[string]bool, len(products))
	for _, info := range *response.Items {
		pid := id(info.ID)
		index, ok := wanted[pid]
		if !ok || seen[pid] || info.OfferID != products[index].OfferID {
			return errors.New("product information identity mismatch")
		}
		if len(info.Name) > 4096 || strings.ContainsRune(info.Name, 0) {
			return errors.New("invalid product name")
		}
		products[index].Name = info.Name
		seen[pid] = true
	}
	return nil
}
