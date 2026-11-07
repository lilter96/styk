package ozon

import "strconv"

// Delivery provider identifiers are signed int64 observations in the published
// v3DeliveryMethod contract. Negative values have no verified carrier meaning;
// preserve their exact value without changing the strict order/SKU/warehouse IDs.
func orderDeliveryProviderID(raw []byte) (string, error) {
	value := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var err error
		value, err = orderString(raw)
		if err != nil {
			return "", errOrderPayload
		}
	}
	if value == "" && len(raw) > 0 {
		return value, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(id, 10) != value {
		return "", errOrderPayload
	}
	return value, nil
}
