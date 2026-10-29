package moysklad

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lilter96/styk/internal/catalog"
)

type catalogCharacteristic struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

type catalogSalePrice struct {
	Value     json.Number      `json:"value"`
	Currency  catalogReference `json:"currency"`
	PriceType catalogReference `json:"priceType"`
}

// Writable facts contain existing UUID references and values only. Provider
// display names and metadata remain curated observations in separate extensions.
func canonicalCatalogCharacteristics(raw json.RawMessage) (json.RawMessage, error) {
	var rows []catalogCharacteristic
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 || len(rows) > 100 {
		return nil, ErrCatalogInvalid
	}
	seen := make(map[string]bool, len(rows))
	for i, row := range rows {
		row.ID = strings.ToLower(row.ID)
		if !orderUUID.MatchString(row.ID) || row.Value == "" || utf8.RuneCountInString(row.Value) > 255 || strings.ContainsRune(row.Value, 0) || seen[row.ID] {
			return nil, ErrCatalogInvalid
		}
		seen[row.ID] = true
		rows[i] = row
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return json.Marshal(rows)
}

func (c *CatalogClient) catalogObservedField(key string, raw json.RawMessage) catalog.Value {
	value := catalogObservedValue(raw)
	if value.State != catalog.StateKnown {
		return value
	}
	var canonical json.RawMessage
	var err error
	switch key {
	case "salePrices":
		canonical, err = c.canonicalCatalogPrices(raw)
	case "barcodes":
		canonical, err = canonicalCatalogBarcodes(raw)
	case "productFolder", "uom":
		kind := map[string]string{"productFolder": "productfolder", "uom": "uom"}[key]
		canonical, err = c.canonicalCatalogReference(raw, "entity/"+kind, kind)
	case "attributes":
		canonical, err = c.canonicalCatalogAttributes(raw)
	default:
		return value
	}
	return catalogCanonicalObservation(canonical, err)
}

func catalogCanonicalObservation(raw json.RawMessage, err error) catalog.Value {
	if err != nil {
		return catalog.Value{State: catalog.StateUnknown}
	}
	return catalogObservedValue(raw)
}

func canonicalCatalogSelection(value catalog.Value, raw json.RawMessage, err error) bool {
	return err == nil && catalog.EqualValue(value, catalog.Value{State: catalog.StateKnown, Data: raw})
}

func (c *CatalogClient) canonicalCatalogReference(raw json.RawMessage, path, kind string) (json.RawMessage, error) {
	var reference catalogReference
	if json.Unmarshal(raw, &reference) != nil {
		return nil, ErrCatalogIdentity
	}
	id, err := c.referenceID(reference, path, kind)
	if err != nil {
		return nil, err
	}
	return json.Marshal(c.referenceAt(path, kind, id))
}

func (c *CatalogClient) canonicalCatalogPrices(raw json.RawMessage) (json.RawMessage, error) {
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 || len(rows) > 100 {
		return nil, ErrCatalogInvalid
	}
	prices := make([]catalogSalePrice, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		price, id, err := c.canonicalCatalogPrice(row)
		if err != nil || seen[id] {
			return nil, ErrCatalogInvalid
		}
		seen[id] = true
		prices = append(prices, price)
	}
	sort.Slice(prices, func(i, j int) bool { return prices[i].PriceType.Meta.Href < prices[j].PriceType.Meta.Href })
	return json.Marshal(prices)
}

func (c *CatalogClient) canonicalCatalogPrice(raw json.RawMessage) (catalogSalePrice, string, error) {
	id, err := c.catalogPriceIdentity(raw)
	if err != nil {
		return catalogSalePrice{}, "", err
	}
	var row catalogSalePrice
	if json.Unmarshal(raw, &row) != nil {
		return catalogSalePrice{}, "", ErrCatalogInvalid
	}
	amount, err := catalogPlainNumber(row.Value)
	if err != nil {
		return catalogSalePrice{}, "", err
	}
	currency, err := c.referenceID(row.Currency, "entity/currency", "currency")
	if err != nil {
		return catalogSalePrice{}, "", err
	}
	row.Value, row.Currency = json.Number(amount), c.reference("currency", currency)
	row.PriceType = c.referenceAt("context/companysettings/pricetype", "pricetype", id)
	return row, id, nil
}

func catalogPlainNumber(number json.Number) (string, error) {
	normal, ok := catalogDecimal(number.String())
	if !ok || normal == "0" {
		if normal == "0" {
			return "0", nil
		}
		return "", ErrCatalogInvalid
	}
	parts := strings.SplitN(normal, "e", 2)
	exponent, err := strconv.Atoi(parts[1])
	if err != nil || exponent < -64 || exponent > 64 || len(parts[0]) > 64 {
		return "", ErrCatalogInvalid
	}
	sign, digits := "", parts[0]
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	if exponent >= 0 {
		return sign + digits + strings.Repeat("0", exponent), nil
	}
	point := len(digits) + exponent
	if point > 0 {
		return sign + digits[:point] + "." + digits[point:], nil
	}
	return sign + "0." + strings.Repeat("0", -point) + digits, nil
}

func canonicalCatalogBarcodes(raw json.RawMessage) (json.RawMessage, error) {
	var rows []map[string]string
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 || len(rows) > 100 {
		return nil, ErrCatalogInvalid
	}
	keys := make([]string, 0, len(rows))
	byKey := make(map[string]map[string]string, len(rows))
	for _, row := range rows {
		if len(row) != 1 {
			return nil, ErrCatalogInvalid
		}
		for kind, value := range row {
			key := kind + ":" + value
			if !validCatalogBarcode(kind, value) || byKey[key] != nil {
				return nil, ErrCatalogInvalid
			}
			keys, byKey[key] = append(keys, key), row
		}
	}
	sort.Strings(keys)
	for i, key := range keys {
		rows[i] = byKey[key]
	}
	return json.Marshal(rows)
}

func (c *CatalogClient) canonicalCatalogAttributes(raw json.RawMessage) (json.RawMessage, error) {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 || len(rows) > 100 {
		return nil, ErrCatalogInvalid
	}
	type attribute struct {
		catalogReference
		Value json.RawMessage `json:"value"`
	}
	attributes := make([]attribute, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		var reference catalogReference
		if json.Unmarshal(row["meta"], &reference.Meta) != nil {
			return nil, ErrCatalogIdentity
		}
		id, err := c.referenceID(reference, "entity/product/metadata/attributes", "attributemetadata")
		if err != nil || seen[id] {
			return nil, ErrCatalogIdentity
		}
		value, err := canonicalCatalogScalar(row["value"])
		if err != nil {
			return nil, err
		}
		seen[id] = true
		attributes = append(attributes, attribute{catalogReference: c.referenceAt("entity/product/metadata/attributes", "attributemetadata", id), Value: value})
	}
	sort.Slice(attributes, func(i, j int) bool { return attributes[i].Meta.Href < attributes[j].Meta.Href })
	return json.Marshal(attributes)
}

func canonicalCatalogScalar(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, ErrCatalogInvalid
	}
	switch typed := value.(type) {
	case json.Number:
		number, err := catalogPlainNumber(typed)
		return json.RawMessage(number), err
	case nil, string, bool:
		return json.Marshal(value)
	default:
		return nil, ErrCatalogInvalid
	}
}
