package ozon

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

var errOrderPayload = errors.New("invalid Ozon order response")

type orderFields struct {
	raw   map[string]json.RawMessage
	err   *error
	drift *[]orders.Drift
	path  string
}

func newOrderFields(data []byte) orderFields {
	var err error
	drift := make([]orders.Drift, 0)
	fields := orderFields{err: &err, drift: &drift, path: "$"}
	if json.Unmarshal(data, &fields.raw) != nil || fields.raw == nil {
		fields.fail()
	}
	return fields
}

func (f orderFields) fail() { *f.err = errOrderPayload }

func (f orderFields) objectPresence(key string) orders.Presence {
	data, present := f.raw[key]
	if !present {
		return orders.PresenceMissing
	}
	if bytes.Equal(data, []byte("null")) {
		return orders.PresenceNull
	}
	if len(f.object(key).raw) == 0 {
		return orders.PresenceEmpty
	}
	return orders.PresenceValue
}

func (f orderFields) object(key string) orderFields {
	child := orderFields{err: f.err, drift: f.drift, path: f.path + "." + key}
	data := f.raw[key]
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return child
	}
	if json.Unmarshal(data, &child.raw) != nil || child.raw == nil {
		f.fail()
	}
	return child
}

func (f orderFields) array(key string) []orderFields {
	data := f.raw[key]
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil
	}
	var rows []json.RawMessage
	if json.Unmarshal(data, &rows) != nil || rows == nil {
		f.fail()
		return nil
	}
	result := make([]orderFields, 0, len(rows))
	for _, row := range rows {
		child := newOrderFields(row)
		if *child.err != nil {
			f.fail()
		}
		child.err = f.err
		child.drift, child.path = f.drift, f.path+"."+key+"[]"
		result = append(result, child)
	}
	return result
}

func orderField[T any](f orderFields, key string, decode func([]byte) (T, error)) orders.Field[T] {
	data, present := f.raw[key]
	if !present {
		return orders.Field[T]{Presence: orders.PresenceMissing}
	}
	if bytes.Equal(data, []byte("null")) {
		return orders.Field[T]{Presence: orders.PresenceNull}
	}
	value, err := decode(data)
	if err != nil {
		f.fail()
		return orders.Field[T]{Presence: orders.PresenceMissing}
	}
	return orders.Present(value)
}

func orderString(data []byte) (string, error) {
	var value string
	err := json.Unmarshal(data, &value)
	return value, err
}

func orderBool(data []byte) (bool, error) {
	var value bool
	err := json.Unmarshal(data, &value)
	return value, err
}

func orderID(data []byte) (string, error) {
	value := string(data)
	if len(data) > 0 && data[0] == '"' {
		var err error
		value, err = orderString(data)
		if err != nil {
			return "", errOrderPayload
		}
	}
	if value == "" {
		return value, nil
	}
	if len(value) > 128 || strings.Trim(value, "0123456789") != "" || (len(value) > 1 && value[0] == '0') {
		return "", errOrderPayload
	}
	return value, nil
}

func orderDecimal(data []byte) (orders.Decimal, error) {
	value := string(data)
	if len(data) > 0 && data[0] == '"' {
		var err error
		value, err = orderString(data)
		if err != nil {
			return "", errOrderPayload
		}
	}
	decimal := orders.Decimal(value)
	if decimal.Validate() != nil {
		return "", errOrderPayload
	}
	return decimal, nil
}

func orderTime(data []byte) (time.Time, error) {
	value, err := orderString(data)
	if err != nil {
		return time.Time{}, errOrderPayload
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errOrderPayload
	}
	return parsed.UTC(), nil
}

func (f orderFields) text(key string) orders.Field[string]  { return orderField(f, key, orderString) }
func (f orderFields) id(key string) orders.Field[string]    { return orderField(f, key, orderID) }
func (f orderFields) boolean(key string) orders.Field[bool] { return orderField(f, key, orderBool) }
func (f orderFields) decimal(key string) orders.Field[orders.Decimal] {
	if bytes.Equal(f.raw[key], []byte(`""`)) {
		return orders.Field[orders.Decimal]{Presence: orders.PresenceEmpty}
	}
	return orderField(f, key, orderDecimal)
}

func (f orderFields) timestamp(key string) orders.Field[time.Time] {
	data := f.raw[key]
	if bytes.Equal(data, []byte(`""`)) {
		return orders.Field[time.Time]{Presence: orders.PresenceEmpty}
	}
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return orderField(f, key, orderTime)
	}
	value, err := orderTime(data)
	if err != nil {
		*f.drift = append(*f.drift, orders.Drift{Path: f.path + "." + key, Code: "unverified_timestamp"})
		return orders.Field[time.Time]{Presence: orders.PresenceMissing}
	}
	return orders.Present(value)
}

func (f orderFields) strings(key string) []string {
	var result []string
	data := f.raw[key]
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil
	}
	if json.Unmarshal(data, &result) != nil {
		f.fail()
	}
	return result
}

func orderKnown[T any](field orders.Field[T]) T {
	if field.IsKnown() {
		return *field.Value
	}
	var zero T
	return zero
}
