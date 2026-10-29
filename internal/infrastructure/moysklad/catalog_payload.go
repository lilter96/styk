package moysklad

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lilter96/styk/internal/catalog"
)

var catalogMarker = regexp.MustCompile(`^[A-Za-z0-9:_-]{1,255}$`)

func validateCatalogCommand(cmd catalog.Command) error {
	if err := catalog.ValidateCommand(cmd); err != nil {
		return err
	}
	if err := catalogAccount(cmd.Scope, cmd.Target.Account); err != nil {
		return err
	}
	if cmd.ID == "" || len(cmd.ID) > 200 || strings.ContainsAny(cmd.ID, "\r\n") || !catalogSourceAllowed(cmd) {
		return ErrCatalogInvalid
	}
	if cmd.Target.EntityType != "product" && cmd.Target.EntityType != "variant" {
		return ErrCatalogIdentity
	}
	if cmd.Target.OfferID != "" || cmd.Target.SKU != "" {
		return ErrCatalogIdentity
	}
	if cmd.Action == catalog.ActionCreate {
		if cmd.Target.ID != "" || !orderUUID.MatchString(cmd.CreateID) || !catalogMarker.MatchString(cmd.CreateExternalCode) {
			return ErrCatalogInvalid
		}
		return nil
	}
	if cmd.Action != catalog.ActionUpdate && cmd.Action != catalog.ActionArchive {
		return ErrCatalogInvalid
	}
	if err := catalogIdentity(cmd.Target.Account, cmd.Target); err != nil {
		return err
	}
	if cmd.ExpectedTargetRevision == "" || cmd.CreateID != "" || cmd.CreateExternalCode != "" {
		return ErrCatalogInvalid
	}
	return nil
}

func catalogSourceAllowed(cmd catalog.Command) bool {
	account := cmd.Source.Account
	if account.Provider == catalog.ProviderMoySklad {
		return account.ID == cmd.Scope.MoyskladAccount
	}
	return account.Provider == catalog.ProviderOzon && account.ID == cmd.Scope.OzonAccount
}

func (c *CatalogClient) catalogPayload(cmd catalog.Command) (map[string]any, error) {
	if err := validateCatalogCommand(cmd); err != nil {
		return nil, err
	}
	body := make(map[string]any, len(cmd.Values)+len(cmd.Extensions)+3)
	for field, value := range cmd.Values {
		if err := c.catalogPayloadField(body, cmd.Target.EntityType, field, value); err != nil {
			return nil, err
		}
	}
	if err := catalogPayloadExtensions(body, cmd); err != nil {
		return nil, err
	}
	if cmd.Action == catalog.ActionArchive {
		if len(body) != 0 {
			return nil, ErrCatalogInvalid
		}
		body["archived"] = true
		return body, nil
	}
	if cmd.Action == catalog.ActionCreate {
		return c.catalogCreatePayload(body, cmd)
	}
	if len(body) == 0 || cmd.Parent != nil {
		return nil, ErrCatalogInvalid
	}
	return body, nil
}

func (c *CatalogClient) catalogPayloadField(body map[string]any, kind string, field catalog.Field, value catalog.Value) error {
	if field == catalog.FieldName || field == catalog.FieldDescription || field == catalog.FieldArticle {
		text, err := catalogTextValue(value, field != catalog.FieldName)
		if err != nil || utf8.RuneCountInString(text) > map[catalog.Field]int{catalog.FieldName: 255, catalog.FieldDescription: 4096, catalog.FieldArticle: 255}[field] {
			return ErrCatalogInvalid
		}
		body[string(field)] = text
		return nil
	}
	if field == catalog.FieldPrice {
		return c.catalogPricePayload(body, value)
	}
	if field == catalog.FieldBarcode || field == catalog.FieldBarcodes {
		return catalogBarcodePayload(body, value)
	}
	if kind != "product" {
		return ErrCatalogInvalid
	}
	switch field {
	case catalog.FieldCategory:
		return c.catalogReferencePayload(body, "productFolder", "productfolder", value)
	case catalog.FieldUnit:
		return c.catalogReferencePayload(body, "uom", "uom", value)
	case catalog.FieldAttributes:
		return c.catalogAttributesPayload(body, value)
	default:
		return ErrCatalogInvalid
	}
}

func catalogTextValue(value catalog.Value, clear bool) (string, error) {
	if value.State == catalog.StateClear && clear && len(value.Data) == 0 {
		return "", nil
	}
	var text string
	if value.State != catalog.StateKnown || json.Unmarshal(value.Data, &text) != nil || !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 || (!clear && strings.TrimSpace(text) == "") {
		return "", ErrCatalogInvalid
	}
	return text, nil
}

func (c *CatalogClient) catalogCreatePayload(body map[string]any, cmd catalog.Command) (map[string]any, error) {
	body["externalCode"] = cmd.CreateExternalCode
	if cmd.Target.EntityType == "product" {
		name, ok := body["name"].(string)
		if !ok || name == "" || cmd.Parent != nil || body["characteristics"] != nil {
			return nil, ErrCatalogInvalid
		}
		body["syncId"] = cmd.CreateID
		return body, nil
	}
	if cmd.Parent == nil || catalogIdentity(cmd.Target.Account, *cmd.Parent) != nil || cmd.Parent.EntityType != "product" || body["characteristics"] == nil {
		return nil, ErrCatalogIdentity
	}
	body["product"] = c.reference("product", cmd.Parent.ID)
	return body, nil
}

func catalogPayloadExtensions(body map[string]any, cmd catalog.Command) error {
	for key, value := range cmd.Extensions {
		switch key {
		case "moysklad.code", "moysklad.article":
			key = strings.TrimPrefix(key, "moysklad.")
			if _, duplicate := body[key]; duplicate {
				return ErrCatalogInvalid
			}
			text, err := catalogTextValue(value, true)
			if err != nil || utf8.RuneCountInString(text) > 255 {
				return ErrCatalogInvalid
			}
			body[key] = text
		case "moysklad.characteristics":
			if cmd.Target.EntityType != "variant" || value.State != catalog.StateKnown || validateCatalogCharacteristics(value.Data) != nil {
				return ErrCatalogInvalid
			}
			body["characteristics"] = json.RawMessage(value.Data)
		default:
			return ErrCatalogInvalid
		}
	}
	return nil
}

func validateCatalogCharacteristics(raw json.RawMessage) error {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 || len(rows) > 100 {
		return ErrCatalogInvalid
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		var id, name, value string
		if json.Unmarshal(row["value"], &value) != nil || utf8.RuneCountInString(value) > 255 || value == "" || len(row) < 2 || len(row) > 3 {
			return ErrCatalogInvalid
		}
		if rawID, ok := row["id"]; ok && (json.Unmarshal(rawID, &id) != nil || !orderUUID.MatchString(id)) {
			return ErrCatalogInvalid
		}
		if rawName, ok := row["name"]; ok && (json.Unmarshal(rawName, &name) != nil || name == "" || utf8.RuneCountInString(name) > 255) {
			return ErrCatalogInvalid
		}
		if (id == "" && name == "") || seen[id+":"+name] {
			return ErrCatalogInvalid
		}
		for key := range row {
			if key != "id" && key != "name" && key != "value" {
				return ErrCatalogInvalid
			}
		}
		seen[id+":"+name] = true
	}
	return nil
}

func (c *CatalogClient) catalogReferencePayload(body map[string]any, key, kind string, value catalog.Value) error {
	if value.State == catalog.StateClear && len(value.Data) == 0 {
		body[key] = nil
		return nil
	}
	var r catalogReference
	if value.State != catalog.StateKnown || json.Unmarshal(value.Data, &r) != nil {
		return ErrCatalogInvalid
	}
	id, err := c.referenceID(r, "entity/"+kind, kind)
	if err != nil {
		return err
	}
	body[key] = c.reference(kind, id)
	return nil
}

func (c *CatalogClient) catalogPricePayload(body map[string]any, value catalog.Value) error {
	var rows []json.RawMessage
	if value.State != catalog.StateKnown || json.Unmarshal(value.Data, &rows) != nil || len(rows) == 0 || len(rows) > 100 {
		return ErrCatalogInvalid
	}
	seen := make(map[string]bool, len(rows))
	for _, raw := range rows {
		id, err := c.catalogPriceIdentity(raw)
		if err != nil || seen[id] {
			return ErrCatalogInvalid
		}
		seen[id] = true
	}
	body["salePrices"] = json.RawMessage(value.Data)
	return nil
}

func (c *CatalogClient) catalogPriceIdentity(raw json.RawMessage) (string, error) {
	var row struct {
		Value     json.Number      `json:"value"`
		Currency  catalogReference `json:"currency"`
		PriceType catalogReference `json:"priceType"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &row) != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 3 {
		return "", ErrCatalogInvalid
	}
	amount, err := strconv.ParseFloat(row.Value.String(), 64)
	if err != nil || amount < 0 || amount > 1e15 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return "", ErrCatalogInvalid
	}
	if _, err := c.referenceID(row.Currency, "entity/currency", "currency"); err != nil {
		return "", err
	}
	return c.referenceID(row.PriceType, "context/companysettings/pricetype", "pricetype")
}

func catalogBarcodePayload(body map[string]any, value catalog.Value) error {
	if _, duplicate := body["barcodes"]; duplicate {
		return ErrCatalogInvalid
	}
	if value.State == catalog.StateClear && len(value.Data) == 0 {
		body["barcodes"] = []any{}
		return nil
	}
	var rows []map[string]string
	if value.State != catalog.StateKnown || json.Unmarshal(value.Data, &rows) != nil || len(rows) == 0 || len(rows) > 100 {
		return ErrCatalogInvalid
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if len(row) != 1 {
			return ErrCatalogInvalid
		}
		for kind, code := range row {
			if !validCatalogBarcode(kind, code) || seen[kind+":"+code] {
				return ErrCatalogInvalid
			}
			seen[kind+":"+code] = true
		}
	}
	body["barcodes"] = json.RawMessage(value.Data)
	return nil
}

func validCatalogBarcode(kind, code string) bool {
	if kind == "code128" {
		return len(code) > 0 && len(code) <= 255 && strings.TrimFunc(code, func(r rune) bool { return r >= 32 && r <= 126 }) == ""
	}
	length := map[string]int{"ean13": 13, "ean8": 8, "gtin": 14, "upc": 12}[kind]
	return length != 0 && len(code) == length && strings.Trim(code, "0123456789") == ""
}

func (c *CatalogClient) catalogAttributesPayload(body map[string]any, value catalog.Value) error {
	var rows []map[string]json.RawMessage
	if value.State != catalog.StateKnown || json.Unmarshal(value.Data, &rows) != nil || len(rows) == 0 || len(rows) > 100 {
		return ErrCatalogInvalid
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		var r catalogReference
		if json.Unmarshal(row["meta"], &r.Meta) != nil || len(row) != 2 || row["value"] == nil {
			return ErrCatalogInvalid
		}
		id, err := c.referenceID(r, "entity/product/metadata/attributes", "attributemetadata")
		if err != nil || seen[id] || !catalogScalar(row["value"]) {
			return ErrCatalogInvalid
		}
		seen[id] = true
	}
	body["attributes"] = json.RawMessage(value.Data)
	return nil
}

func catalogScalar(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch value.(type) {
	case nil, string, bool, float64:
		return true
	default:
		return false
	}
}
