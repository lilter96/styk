package ozon

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/lilter96/styk/internal/catalog"
)

// Creation accepts reviewed provider facts only. It supplies no VAT, category,
// dimensions, brand, marking, certification, or other legal attribute defaults.
type catalogCreateItem struct {
	OfferID  string `json:"offer_id"`
	Name     string `json:"name"`
	Price    string `json:"price"`
	Currency string `json:"currency_code"`
	VAT      string `json:"vat"`
	CategorySelection
	Dimensions
	Attributes []CatalogAttribute `json:"attributes"`
	Barcode    string             `json:"barcode,omitempty"`
	Images     []string           `json:"images,omitempty"`
}

var catalogDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})(\.[0-9]{1,4})?$`)

func (c *CatalogClient) catalogCreatePayload(ctx context.Context, command catalog.Command) (string, []byte, error) {
	item, err := catalogCreateFacts(command)
	if err != nil {
		return "", nil, err
	}
	if err := c.catalogOfferAbsent(ctx, item.OfferID); err != nil {
		return "", nil, err
	}
	tree, err := c.CategoryTree(ctx)
	if err != nil {
		return "", nil, err
	}
	if !categoryContains(tree, item.CategorySelection, 0, false, 0) {
		return "", nil, catalogFactError(catalog.FieldCategory, "CATEGORY_NOT_CREATABLE", "The category and type are not an enabled leaf in the current Ozon tree", ErrCatalogUnsupported)
	}
	schema, err := c.CategoryAttributes(ctx, item.CategorySelection)
	if err != nil {
		return "", nil, err
	}
	if err := c.validateCreateAttributes(ctx, item, schema); err != nil {
		return "", nil, err
	}
	payload, err := json.Marshal(struct {
		Items []catalogCreateItem `json:"items"`
	}{Items: []catalogCreateItem{item}})
	return catalogImportPath, payload, err
}

func catalogCreateFacts(command catalog.Command) (catalogCreateItem, error) {
	item := catalogCreateItem{OfferID: command.Target.OfferID}
	if err := requiredCreateFacts(command); err != nil {
		return item, err
	}
	for field, value := range command.Values {
		if value.State != catalog.StateKnown {
			return item, ErrCatalogUnsupported
		}
		switch field {
		case catalog.FieldName, catalog.FieldDescription, catalog.FieldPrice, catalog.FieldCurrency, catalog.FieldBarcode, catalog.FieldImages, catalog.FieldCategory, catalog.FieldDimensions, catalog.FieldAttributes:
		default:
			return item, ErrCatalogUnsupported
		}
	}
	if len(command.Extensions) != 1 {
		return item, ErrCatalogUnsupported
	}
	if err := catalogCreateScalars(command, &item); err != nil {
		return item, err
	}
	if decodeCatalogFacts(command.Values[catalog.FieldCategory].Data, &item.CategorySelection) != nil || decodeCatalogFacts(command.Values[catalog.FieldDimensions].Data, &item.Dimensions) != nil || decodeCatalogFacts(command.Values[catalog.FieldAttributes].Data, &item.Attributes) != nil {
		return item, catalog.ErrInvalidValue
	}
	if !item.valid() || item.DescriptionCategoryID <= 0 || item.TypeID <= 0 || len(item.Attributes) > 500 {
		return item, catalog.ErrInvalidValue
	}
	if err := catalogCreateOptional(command, &item); err != nil {
		return item, err
	}
	return item, nil
}

func catalogCreateScalars(command catalog.Command, item *catalogCreateItem) error {
	var err error
	item.Name, err = command.Values[catalog.FieldName].StringValue()
	if err != nil || !catalogTextValid(item.Name, 500) {
		return catalog.ErrInvalidValue
	}
	item.Price, err = command.Values[catalog.FieldPrice].StringValue()
	if err != nil || !catalogDecimal.MatchString(item.Price) || strings.Trim(item.Price, "0.") == "" {
		return catalog.ErrInvalidValue
	}
	item.Currency, err = command.Values[catalog.FieldCurrency].StringValue()
	if err != nil || len(item.Currency) != 3 || strings.Trim(item.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return catalog.ErrInvalidValue
	}
	item.VAT, err = command.Extensions["vat"].StringValue()
	if err != nil || !catalogDecimal.MatchString(item.VAT) {
		return catalog.ErrInvalidValue
	}
	return nil
}

func catalogCreateOptional(command catalog.Command, item *catalogCreateItem) error {
	if value, ok := command.Values[catalog.FieldBarcode]; ok {
		barcode, err := value.StringValue()
		if err != nil || !catalogTextValid(barcode, 128) {
			return catalog.ErrInvalidValue
		}
		item.Barcode = barcode
	}
	if value, ok := command.Values[catalog.FieldImages]; ok {
		if decodeCatalogJSON(value.Data, &item.Images) != nil || !catalogImagesValid(item.Images) {
			return catalog.ErrInvalidValue
		}
	}
	if value, ok := command.Values[catalog.FieldDescription]; ok {
		description, err := value.StringValue()
		if err != nil || !validPatchText(catalog.FieldDescription, description) {
			return catalog.ErrInvalidValue
		}
		for _, attribute := range item.Attributes {
			if attribute.ID == 4191 {
				return catalog.ErrInvalidValue
			}
		}
		item.Attributes = append(item.Attributes, CatalogAttribute{ID: 4191, Values: []AttributeValue{{Value: description}}})
	}
	return nil
}

func (d Dimensions) valid() bool {
	for _, value := range []int64{d.Height, d.Depth, d.Width, d.Weight} {
		if value <= 0 || value > 2147483647 {
			return false
		}
	}
	return (d.DimensionUnit == "mm" || d.DimensionUnit == "cm" || d.DimensionUnit == "in") && (d.WeightUnit == "g" || d.WeightUnit == "kg" || d.WeightUnit == "lb")
}

func catalogImagesValid(images []string) bool {
	if len(images) == 0 || len(images) > 50 {
		return false
	}
	seen := make(map[string]bool, len(images))
	for _, image := range images {
		u, err := url.Parse(image)
		if err != nil || len(image) > 2048 || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || seen[image] {
			return false
		}
		host := strings.ToLower(u.Hostname())
		if net.ParseIP(host) != nil || !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
			return false
		}
		seen[image] = true
	}
	return true
}

func (c *CatalogClient) catalogOfferAbsent(ctx context.Context, offer string) error {
	var response struct {
		Items *[]catalogWire `json:"items"`
	}
	if err := c.catalogPost(ctx, productInfoPath, map[string]any{"offer_id": []string{offer}}, &response); err != nil {
		return err
	}
	if response.Items == nil {
		return ErrCatalogContract
	}
	if len(*response.Items) != 0 {
		return catalog.ErrIdentityMismatch
	}
	return nil
}

func categoryContains(nodes []CategoryNode, selection CategorySelection, parent int64, disabled bool, depth int) bool {
	if depth > 32 {
		return false
	}
	for _, node := range nodes {
		category := parent
		if node.DescriptionCategoryID != 0 {
			category = node.DescriptionCategoryID
		}
		blocked := disabled || node.Disabled
		if node.TypeID == selection.TypeID && category == selection.DescriptionCategoryID && len(node.Children) == 0 && !blocked {
			return true
		}
		if categoryContains(node.Children, selection, category, blocked, depth+1) {
			return true
		}
	}
	return false
}

func (c *CatalogClient) validateCreateAttributes(ctx context.Context, item catalogCreateItem, schema []CategoryAttribute) error {
	byID := make(map[int64]CategoryAttribute, len(schema))
	for _, attribute := range schema {
		byID[attribute.ID] = attribute
	}
	seen := make(map[int64]bool, len(item.Attributes))
	for _, attribute := range item.Attributes {
		definition, ok := byID[attribute.ID]
		if !ok || seen[attribute.ID] || attribute.ComplexID != 0 || definition.ComplexID != 0 || len(attribute.Values) == 0 || len(attribute.Values) > 100 {
			return catalog.ErrInvalidValue
		}
		if (!definition.Collection && len(attribute.Values) != 1) || (definition.MaxValueCount > 0 && len(attribute.Values) > definition.MaxValueCount) {
			return catalog.ErrInvalidValue
		}
		seen[attribute.ID] = true
		if attribute.ID == 4180 && (len(attribute.Values) != 1 || attribute.Values[0].Value != item.Name) {
			return catalog.ErrInvalidValue
		}
		if err := c.validateAttributeValues(ctx, item.CategorySelection, attribute, definition); err != nil {
			return err
		}
	}
	for _, attribute := range schema {
		if attribute.Required && attribute.ID != 8229 && attribute.ID != 4180 && !seen[attribute.ID] {
			return missingAttributeError(attribute.ID)
		}
	}
	return nil
}

func (c *CatalogClient) validateAttributeValues(ctx context.Context, selection CategorySelection, attribute CatalogAttribute, definition CategoryAttribute) error {
	for _, value := range attribute.Values {
		if !catalogTextValid(value.Value, 32768) {
			return catalog.ErrInvalidValue
		}
		if attribute.ID == 8229 && value.DictionaryValueID != selection.TypeID {
			return catalog.ErrInvalidValue
		}
		if definition.DictionaryID == 0 {
			if value.DictionaryValueID != 0 {
				return catalog.ErrInvalidValue
			}
			continue
		}
		if value.DictionaryValueID <= 0 {
			return catalog.ErrInvalidValue
		}
		page, err := c.AttributeValues(ctx, selection, attribute.ID, value.DictionaryValueID-1, 1)
		if err != nil {
			return err
		}
		if len(page.Values) != 1 || page.Values[0].ID != value.DictionaryValueID || page.Values[0].Value != value.Value {
			return catalog.ErrInvalidValue
		}
	}
	return nil
}
