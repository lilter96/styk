package moysklad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

// CatalogMetadata exposes provider identifiers and exact money/barcode formats.
// SalePrices values are minor units; no currency conversion is inferred.
type CatalogMetadata struct {
	Account         catalog.Account    `json:"account"`
	Folders         []CatalogFolder    `json:"folders"`
	Units           []CatalogUnit      `json:"units"`
	Attributes      []CatalogAttribute `json:"attributes"`
	Characteristics []CatalogAttribute `json:"characteristics"`
	PriceTypes      []CatalogPriceType `json:"price_types"`
	BarcodeFormats  []string           `json:"barcode_formats"`
	ObservedAt      time.Time          `json:"observed_at"`
}

type CatalogFolder struct {
	ID, Name, Code, ParentID string
	Archived                 bool
}

type CatalogUnit struct {
	ID, Name, Code, Description string
}

type CatalogAttribute struct {
	ID, Name, Type string
	Required       bool
}

type CatalogPriceType struct {
	ID, Name, ExternalCode string
}

type catalogMetadataRow struct {
	catalogReference
	ID, AccountID, Name, Code, Description, ExternalCode string
	Type                                                 string
	Required, Archived                                   bool
	ProductFolder                                        *catalogReference
}

func (c *CatalogClient) ReadCatalogMetadata(ctx context.Context, scope catalog.Scope, account catalog.Account) (CatalogMetadata, error) {
	if err := catalogAccount(scope, account); err != nil {
		return CatalogMetadata{}, err
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx, account); err != nil {
		return CatalogMetadata{}, err
	}
	out := CatalogMetadata{Account: account, ObservedAt: time.Now().UTC(), BarcodeFormats: []string{"ean13", "ean8", "code128", "gtin", "upc"}}
	if err := c.readCatalogDictionaries(ctx, &out); err != nil {
		return CatalogMetadata{}, err
	}
	if err := c.readCatalogPriceTypes(ctx, &out); err != nil {
		return CatalogMetadata{}, err
	}
	if err := c.readCatalogCharacteristics(ctx, &out); err != nil {
		return CatalogMetadata{}, err
	}
	return out, nil
}

func (c *CatalogClient) readCatalogDictionaries(ctx context.Context, out *CatalogMetadata) error {
	paths := []string{"entity/productfolder", "entity/uom", "entity/product/metadata/attributes"}
	for _, path := range paths {
		seen := make(map[string]bool)
		err := c.client.pages(ctx, path, url.Values{}, func(raw json.RawMessage) error {
			var row catalogMetadataRow
			if json.Unmarshal(raw, &row) != nil || !orderUUID.MatchString(row.ID) || row.Name == "" || (row.AccountID != "" && row.AccountID != out.Account.ID) || seen[row.ID] {
				return ErrCatalogIdentity
			}
			if len(seen) >= 5000 {
				return &LimitError{Resource: "catalog_metadata"}
			}
			seen[row.ID] = true
			return c.appendCatalogDictionary(out, path, row)
		})
		if err != nil {
			return fmt.Errorf("read catalog dictionary: %w", err)
		}
	}
	return nil
}

func (c *CatalogClient) appendCatalogDictionary(out *CatalogMetadata, path string, row catalogMetadataRow) error {
	kind := map[string]string{"entity/productfolder": "productfolder", "entity/uom": "uom", "entity/product/metadata/attributes": "attributemetadata"}[path]
	id, err := c.referenceID(row.catalogReference, path, kind)
	if err != nil || id != row.ID {
		return ErrCatalogIdentity
	}
	switch kind {
	case "productfolder":
		parent := ""
		if row.ProductFolder != nil {
			parent, err = c.referenceID(*row.ProductFolder, "entity/productfolder", "productfolder")
			if err != nil {
				return err
			}
		}
		out.Folders = append(out.Folders, CatalogFolder{ID: id, Name: row.Name, Code: row.Code, ParentID: parent, Archived: row.Archived})
	case "uom":
		out.Units = append(out.Units, CatalogUnit{ID: id, Name: row.Name, Code: row.Code, Description: row.Description})
	case "attributemetadata":
		out.Attributes = append(out.Attributes, CatalogAttribute{ID: id, Name: row.Name, Type: row.Type, Required: row.Required})
	}
	return nil
}

func (c *CatalogClient) readCatalogPriceTypes(ctx context.Context, out *CatalogMetadata) error {
	raw, err := c.client.get(ctx, "context/companysettings/pricetype", nil)
	if err != nil {
		return err
	}
	var rows []catalogMetadataRow
	if json.Unmarshal(raw, &rows) != nil || rows == nil || len(rows) > 100 {
		return ErrCatalogIdentity
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		id, err := c.referenceID(row.catalogReference, "context/companysettings/pricetype", "pricetype")
		if err != nil || id != row.ID || row.Name == "" || seen[id] {
			return ErrCatalogIdentity
		}
		seen[id] = true
		out.PriceTypes = append(out.PriceTypes, CatalogPriceType{ID: id, Name: row.Name, ExternalCode: row.ExternalCode})
	}
	return nil
}

func (c *CatalogClient) readCatalogCharacteristics(ctx context.Context, out *CatalogMetadata) error {
	raw, err := c.client.get(ctx, "entity/variant/metadata", nil)
	if err != nil {
		return err
	}
	var response struct {
		Characteristics *[]catalogMetadataRow `json:"characteristics"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Characteristics == nil || len(*response.Characteristics) > 5000 {
		return ErrCatalogIdentity
	}
	seen := make(map[string]bool, len(*response.Characteristics))
	for _, row := range *response.Characteristics {
		if !orderUUID.MatchString(row.ID) || row.Name == "" || seen[row.ID] {
			return ErrCatalogIdentity
		}
		seen[row.ID] = true
		out.Characteristics = append(out.Characteristics, CatalogAttribute{ID: row.ID, Name: row.Name, Type: row.Type, Required: row.Required})
	}
	return nil
}
