package ozon

import (
	"bytes"
	"encoding/json"
	"github.com/lilter96/styk/internal/orders"
)

func decodeExemplarBoxCount(raw []byte) (int64, error) {
	var count int64
	if json.Unmarshal(raw, &count) != nil || count < 0 || count > 2147483647 {
		return 0, orders.ErrSchemaDrift
	}
	return count, nil
}

func exemplarBoxCount(raw []byte) (orders.Field[int64], error) {
	f := newOrderFields([]byte(`{}`))
	if len(raw) > 0 {
		f.raw["value"] = raw
	}
	result := orderField(f, "value", decodeExemplarBoxCount)
	return result, *f.err
}

func decodeExemplarWeight(raw []byte) (orders.Decimal, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] == '"' {
		return "", orders.ErrSchemaDrift
	}
	return orderDecimal(value)
}
