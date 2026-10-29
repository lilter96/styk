package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/lilter96/styk/internal/catalog"
)

const (
	catalogAttributesPath  = "/v4/product/info/attributes"
	catalogDescriptionPath = "/v1/product/info/description"
	catalogTreePath        = "/v1/description-category/tree"
	catalogSchemaPath      = "/v1/description-category/attribute"
	catalogDictionaryPath  = "/v1/description-category/attribute/values"
	catalogImportInfoPath  = "/v1/product/import/info"
	catalogImportPath      = "/v3/product/import"
	catalogPatchPath       = "/v1/product/attributes/update"
)

// CategorySelection is an explicit provider fact, never inferred from a name.
type CategorySelection struct {
	DescriptionCategoryID int64 `json:"description_category_id"`
	TypeID                int64 `json:"type_id"`
}

type CategoryNode struct {
	DescriptionCategoryID int64          `json:"description_category_id"`
	CategoryName          string         `json:"category_name"`
	TypeID                int64          `json:"type_id"`
	TypeName              string         `json:"type_name"`
	Disabled              bool           `json:"disabled"`
	Children              []CategoryNode `json:"children"`
}

func (node *CategoryNode) UnmarshalJSON(data []byte) error {
	type wire CategoryNode
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ErrCatalogContract
	}
	var presence struct {
		Disabled *bool           `json:"disabled"`
		Children json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return ErrCatalogContract
	}
	if presence.Disabled == nil || len(presence.Children) == 0 || string(presence.Children) == "null" {
		return ErrCatalogContract
	}
	*node = CategoryNode(decoded)
	return nil
}

type CategoryAttribute struct {
	ID                int64  `json:"id"`
	ComplexID         int64  `json:"attribute_complex_id"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	Required          bool   `json:"is_required"`
	Collection        bool   `json:"is_collection"`
	MaxValueCount     int    `json:"max_value_count"`
	DictionaryID      int64  `json:"dictionary_id"`
	CategoryDependent bool   `json:"category_dependent"`
}

func (attribute *CategoryAttribute) UnmarshalJSON(data []byte) error {
	type wire CategoryAttribute
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return ErrCatalogContract
	}
	var presence map[string]json.RawMessage
	if err := json.Unmarshal(data, &presence); err != nil {
		return ErrCatalogContract
	}
	for _, key := range []string{"id", "attribute_complex_id", "type", "is_required", "is_collection", "dictionary_id", "max_value_count"} {
		if value, ok := presence[key]; !ok || string(value) == "null" {
			return ErrCatalogContract
		}
	}
	*attribute = CategoryAttribute(decoded)
	return nil
}

type DictionaryValue struct {
	ID      int64  `json:"id"`
	Value   string `json:"value"`
	Info    string `json:"info"`
	Picture string `json:"picture"`
}

type DictionaryPage struct {
	Values      []DictionaryValue `json:"values"`
	HasNext     bool              `json:"has_next"`
	LastValueID int64             `json:"last_value_id"`
}

type AttributeValue struct {
	DictionaryValueID int64  `json:"dictionary_value_id"`
	Value             string `json:"value"`
}

type CatalogAttribute struct {
	ID        int64            `json:"id"`
	ComplexID int64            `json:"complex_id"`
	Values    []AttributeValue `json:"values"`
}

type Dimensions struct {
	Height        int64  `json:"height"`
	Depth         int64  `json:"depth"`
	Width         int64  `json:"width"`
	DimensionUnit string `json:"dimension_unit"`
	Weight        int64  `json:"weight"`
	WeightUnit    string `json:"weight_unit"`
}

func catalogReadPath(path string) bool {
	switch path {
	case catalogAttributesPath, catalogDescriptionPath, catalogTreePath, catalogSchemaPath, catalogDictionaryPath, catalogImportInfoPath:
		return true
	default:
		return false
	}
}

func (c *CatalogClient) CategoryTree(ctx context.Context) ([]CategoryNode, error) {
	var response struct {
		Result *[]CategoryNode `json:"result"`
	}
	if err := c.catalogPost(ctx, catalogTreePath, map[string]string{"language": "DEFAULT"}, &response); err != nil {
		return nil, err
	}
	if response.Result == nil {
		return nil, ErrCatalogContract
	}
	return *response.Result, nil
}

func (c *CatalogClient) CategoryAttributes(ctx context.Context, selection CategorySelection) ([]CategoryAttribute, error) {
	if selection.DescriptionCategoryID <= 0 || selection.TypeID <= 0 {
		return nil, errors.New("category and type are required")
	}
	var response struct {
		Result *[]CategoryAttribute `json:"result"`
	}
	if err := c.catalogPost(ctx, catalogSchemaPath, selection, &response); err != nil {
		return nil, err
	}
	if response.Result == nil || len(*response.Result) > 1000 {
		return nil, ErrCatalogContract
	}
	seen := make(map[int64]bool, len(*response.Result))
	for _, attribute := range *response.Result {
		if attribute.ID <= 0 || seen[attribute.ID] {
			return nil, ErrCatalogContract
		}
		seen[attribute.ID] = true
	}
	return *response.Result, nil
}

func (c *CatalogClient) AttributeValues(ctx context.Context, selection CategorySelection, attribute, last int64, limit int) (DictionaryPage, error) {
	if selection.DescriptionCategoryID <= 0 || selection.TypeID <= 0 || attribute <= 0 || last < 0 || limit < 1 || limit > 2000 {
		return DictionaryPage{}, errors.New("invalid dictionary request")
	}
	body := map[string]any{"description_category_id": selection.DescriptionCategoryID, "type_id": selection.TypeID, "attribute_id": attribute, "last_value_id": last, "limit": limit, "language": "DEFAULT"}
	var response struct {
		Result  *[]DictionaryValue `json:"result"`
		HasNext *bool              `json:"has_next"`
	}
	if err := c.catalogPost(ctx, catalogDictionaryPath, body, &response); err != nil {
		return DictionaryPage{}, err
	}
	if response.Result == nil || response.HasNext == nil || len(*response.Result) > limit {
		return DictionaryPage{}, ErrCatalogContract
	}
	page := DictionaryPage{Values: *response.Result, HasNext: *response.HasNext, LastValueID: last}
	for _, value := range page.Values {
		if value.ID <= page.LastValueID {
			return DictionaryPage{}, ErrCatalogContract
		}
		page.LastValueID = value.ID
	}
	if page.HasNext && len(page.Values) == 0 {
		return DictionaryPage{}, ErrCatalogContract
	}
	return page, nil
}

func decodeCatalogJSON(data json.RawMessage, out any) error {
	if len(data) == 0 || string(data) == "null" {
		return ErrCatalogContract
	}
	if err := json.Unmarshal(data, out); err != nil {
		return ErrCatalogContract
	}
	return nil
}

func decodeCatalogFacts(data json.RawMessage, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return catalog.ErrInvalidValue
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return catalog.ErrInvalidValue
	}
	return nil
}
