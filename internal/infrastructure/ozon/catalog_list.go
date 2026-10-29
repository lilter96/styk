package ozon

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/lilter96/styk/internal/catalog"
)

type catalogCursor struct {
	Visibility string `json:"visibility"`
	LastID     string `json:"last_id"`
	Offset     int64  `json:"offset"`
	Total      int64  `json:"total"`
}

func decodeCatalogCursor(value string) (catalogCursor, error) {
	if value == "" {
		return catalogCursor{Visibility: "ALL", Total: -1}, nil
	}
	var cursor catalogCursor
	if len(value) > 4096 {
		return cursor, catalog.ErrInvalidValue
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || decodeCatalogFacts(data, &cursor) != nil {
		return cursor, catalog.ErrInvalidValue
	}
	if (cursor.Visibility != "ALL" && cursor.Visibility != "ARCHIVED") || cursor.Offset < 0 || cursor.Total < -1 || cursor.Total > maxRows || (cursor.Offset > 0 && (cursor.LastID == "" || cursor.Offset >= cursor.Total)) {
		return cursor, catalog.ErrInvalidValue
	}
	return cursor, nil
}

func encodeCatalogCursor(cursor catalogCursor) string {
	data, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func (c *CatalogClient) catalogListIdentities(ctx context.Context, account catalog.Account, value string, limit int) ([]catalog.Identity, bool, string, error) {
	cursor, err := decodeCatalogCursor(value)
	if err != nil {
		return nil, false, "", err
	}
	identities := make([]catalog.Identity, 0, limit)
	for range 2 {
		items, next, more, err := c.catalogListPage(ctx, account, cursor, limit-len(identities))
		if err != nil {
			return nil, false, "", err
		}
		identities = append(identities, items...)
		if more {
			return identities, false, encodeCatalogCursor(next), nil
		}
		if cursor.Visibility == "ARCHIVED" {
			return identities, true, "", nil
		}
		cursor = catalogCursor{Visibility: "ARCHIVED", Total: -1}
		if len(identities) == limit {
			return identities, false, encodeCatalogCursor(cursor), nil
		}
	}
	return nil, false, "", ErrCatalogContract
}

func (c *CatalogClient) catalogListPage(ctx context.Context, account catalog.Account, cursor catalogCursor, limit int) ([]catalog.Identity, catalogCursor, bool, error) {
	var response productResponse
	body := map[string]any{"filter": map[string]string{"visibility": cursor.Visibility}, "limit": limit, "last_id": cursor.LastID}
	if err := c.catalogPost(ctx, productPath, body, &response); err != nil {
		return nil, cursor, false, err
	}
	if response.Result == nil || response.Result.Items == nil || response.Result.Total == nil {
		return nil, cursor, false, ErrCatalogContract
	}
	items, total := *response.Result.Items, int64(*response.Result.Total)
	if len(items) > limit || total < 0 || total > maxRows || cursor.Offset+int64(len(items)) > total || (cursor.Total >= 0 && cursor.Total != total) {
		return nil, cursor, false, ErrCatalogContract
	}
	identities := make([]catalog.Identity, 0, len(items))
	for _, item := range items {
		if id(item.ProductID) == "" || item.OfferID == "" {
			return nil, cursor, false, ErrCatalogContract
		}
		identities = append(identities, catalog.Identity{Account: account, EntityType: "product", ID: id(item.ProductID), OfferID: item.OfferID, SKU: id(item.SKU)})
	}
	more := cursor.Offset+int64(len(items)) < total
	if more && (len(items) == 0 || response.Result.LastID == "" || response.Result.LastID == cursor.LastID) {
		return nil, cursor, false, ErrCatalogContract
	}
	next := catalogCursor{Visibility: cursor.Visibility, LastID: response.Result.LastID, Offset: cursor.Offset + int64(len(items)), Total: total}
	return identities, next, more, nil
}
