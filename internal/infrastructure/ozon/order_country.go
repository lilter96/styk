package ozon

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/lilter96/styk/internal/orders"
)

const countryListPath = "/v2/posting/fbs/product/country/list"
const countrySetPath = "/v2/posting/fbs/product/country/set"

func (c *OrdersClient) ReadOrderCountries(ctx context.Context, scope orders.Scope, search string) ([]orders.Country, error) {
	if scope != c.scope || utf8.RuneCountInString(search) > 128 {
		return nil, orders.ErrInvalid
	}
	raw, err := c.orderPost(ctx, countryListPath, map[string]string{"name_search": search})
	if err != nil {
		return nil, err
	}
	var response struct {
		Result []orders.Country `json:"result"`
	}
	if decodeLabelJSON(raw, &response) != nil || response.Result == nil || len(response.Result) > 1000 {
		return nil, orders.ErrSchemaDrift
	}
	seen := make(map[string]bool, len(response.Result))
	for _, country := range response.Result {
		if !orders.CountryISOValid(country.ISOCode) || country.Name == "" || utf8.RuneCountInString(country.Name) > 255 || seen[country.ISOCode] {
			return nil, orders.ErrSchemaDrift
		}
		seen[country.ISOCode] = true
	}
	return response.Result, nil
}
func countrySetPayload(command orders.Command) (map[string]any, error) {
	input := command.PostingAction.Parameters
	if !input.CountryConfirmed || !orders.CountryISOValid(input.CountryISOCode) {
		return nil, orders.ErrInvalid
	}
	sku, err := parseSKU(input.CountrySKU)
	if err != nil {
		return nil, err
	}
	return map[string]any{"posting_number": command.PostingNumber, "product_id": sku, "country_iso_code": input.CountryISOCode}, nil
}
func (c *OrdersClient) countrySetReceipt(command orders.Command, data []byte, status int) (orders.Receipt, error) {
	var response struct {
		ProductID json.RawMessage `json:"product_id"`
		GTD       json.RawMessage `json:"is_gtd_needed"`
	}
	if decodeLabelJSON(data, &response) != nil {
		return orderReceipt(command, orders.StateUnknown, status, "COUNTRY_RESPONSE_UNVERIFIED"), orders.ErrSchemaDrift
	}
	sku, err := orderID(response.ProductID)
	if err != nil || sku != command.PostingAction.Parameters.CountrySKU {
		return orderReceipt(command, orders.StateUnknown, status, "COUNTRY_RESPONSE_TARGET_UNVERIFIED"), orders.ErrSchemaDrift
	}
	fields := newOrderFields([]byte(`{}`))
	if len(response.GTD) > 0 {
		fields.raw["needed"] = response.GTD
	}
	needed := fields.boolean("needed")
	if *fields.err != nil {
		return orderReceipt(command, orders.StateUnknown, status, "COUNTRY_RESPONSE_UNVERIFIED"), orders.ErrSchemaDrift
	}
	receipt := orderReceipt(command, orders.StateAcknowledged, status, "COUNTRY_REQUEST_ACKNOWLEDGED_VALUE_NOT_OBSERVABLE")
	receipt.CountryEvidence = &orders.CountryEvidence{Scope: command.Scope, SKU: sku, GTDNeeded: needed, Meaning: "country_requirement_resolution", TargetFingerprint: command.PostingAction.PackingTargetFingerprint}
	return receipt, nil
}
