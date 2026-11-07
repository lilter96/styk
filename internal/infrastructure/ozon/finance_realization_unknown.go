package ozon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

var financialComponentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func realizationUnknownComponents(root, resultFields, header orderFields, rows []orderFields) ([]orders.RealizationUnknownComponent, error) {
	result, err := unknownRealizationFields(header, -1, "header", []string{"contract_date", "contract_number", "currency_sys_name", "doc_date", "number", "payer_inn", "payer_kpp", "payer_name", "receiver_inn", "receiver_kpp", "receiver_name", "start_date", "stop_date"})
	if err != nil {
		return nil, err
	}
	for _, source := range []struct {
		fields orderFields
		path   string
		keys   []string
	}{{root, "root", []string{"result"}}, {resultFields, "result", []string{"header", "rows"}}} {
		values, err := unknownRealizationFields(source.fields, -1, source.path, source.keys)
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
	}
	for _, row := range rows {
		number, err := strconv.ParseInt(string(row.raw["rowNumber"]), 10, 32)
		if err != nil {
			return nil, orders.ErrSchemaDrift
		}
		for _, source := range []struct {
			fields orderFields
			path   string
			keys   []string
		}{
			{row, "row", []string{"commission_ratio", "delivery_commission", "item", "return_commission", "rowNumber", "seller_price_per_instance"}},
			{row.object("item"), "item", []string{"barcode", "name", "offer_id", "sku"}},
			{row.object("delivery_commission"), "delivery_commission", realizationAmountKeys()},
			{row.object("return_commission"), "return_commission", realizationAmountKeys()},
		} {
			values, err := unknownRealizationFields(source.fields, number, source.path, source.keys)
			if err != nil {
				return nil, err
			}
			result = append(result, values...)
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	slices.SortFunc(result, func(a, b orders.RealizationUnknownComponent) int {
		if a.SourceRow < b.SourceRow {
			return -1
		}
		if a.SourceRow > b.SourceRow {
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	return result, nil
}

func realizationAmountKeys() []string {
	return []string{"amount", "bonus", "commission", "compensation", "price_per_instance", "quantity", "standard_fee", "bank_coinvestment", "pick_up_point_coinvestment", "stars", "total"}
}

func unknownRealizationFields(fields orderFields, row int64, path string, keys []string) ([]orders.RealizationUnknownComponent, error) {
	result := make([]orders.RealizationUnknownComponent, 0)
	for key, raw := range fields.raw {
		if slices.Contains(keys, key) {
			continue
		}
		value, err := unknownRealizationValue(raw)
		if err != nil {
			return nil, err
		}
		if !financialComponentName.MatchString(key) {
			hash := sha256.Sum256([]byte(key))
			key = "field_" + hex.EncodeToString(hash[:])
		}
		value.SourceRow, value.Path = row, path+"."+key
		result = append(result, value)
	}
	return result, nil
}

func unknownRealizationValue(raw json.RawMessage) (orders.RealizationUnknownComponent, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return orders.RealizationUnknownComponent{}, orders.ErrSchemaDrift
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return orders.RealizationUnknownComponent{}, err
	}
	digest := sha256.Sum256(canonical)
	result := orders.RealizationUnknownComponent{Kind: "OPAQUE", ValueDigest: hex.EncodeToString(digest[:])}
	if value == nil {
		result.Kind = "NULL"
		result.Value = orders.Field[orders.Decimal]{Presence: orders.PresenceNull}
	}
	if number, ok := value.(json.Number); ok {
		result.Kind = "NUMBER"
		decimal := orders.Decimal(number)
		if decimal.Validate() == nil {
			result.Value = orders.Present(decimal)
		}
	}
	return result, nil
}
