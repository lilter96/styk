package ozon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/lilter96/styk/internal/orders"
)

type accrualDecoder struct {
	facts   []orders.AccrualFact
	unknown []orders.AccrualUnknownField
	offset  int
}

func (decoder *accrualDecoder) node(raw json.RawMessage, path string, node accrualNode) error {
	if len(decoder.facts) >= 100000 {
		return &LimitError{Resource: "accrual_facts"}
	}
	fact := orders.AccrualFact{Path: path, Kind: node.kind, Presence: orders.PresenceValue}
	if raw == nil {
		fact.Presence = orders.PresenceMissing
		decoder.facts = append(decoder.facts, fact)
		return nil
	}
	if bytes.Equal(raw, []byte("null")) {
		fact.Presence = orders.PresenceNull
		decoder.facts = append(decoder.facts, fact)
		return nil
	}
	switch node.kind {
	case "OBJECT":
		return decoder.object(raw, path, node)
	case "ARRAY":
		return decoder.array(raw, path, node)
	case "STRING", "DECIMAL":
		text, err := orderString(raw)
		if err != nil || len(text) > 8192 {
			return orders.ErrSchemaDrift
		}
		if text == "" {
			fact.Presence = orders.PresenceEmpty
		} else if node.kind == "DECIMAL" {
			value := orders.Decimal(text)
			if value.Validate() != nil {
				return orders.ErrSchemaDrift
			}
			fact.Number = &value
			if value == "0" {
				fact.Presence = orders.PresenceZero
			}
		} else {
			fact.Text = &text
		}
	case "INTEGER":
		value, err := strconv.ParseInt(string(raw), 10, node.bits)
		if err != nil || strconv.FormatInt(value, 10) != string(raw) {
			return orders.ErrSchemaDrift
		}
		decimal := orders.Decimal(raw)
		fact.Number = &decimal
		if value == 0 {
			fact.Presence = orders.PresenceZero
		}
	}
	decoder.facts = append(decoder.facts, fact)
	return nil
}

func (decoder *accrualDecoder) object(raw json.RawMessage, path string, node accrualNode) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return orders.ErrSchemaDrift
	}
	size := len(fields)
	decoder.facts = append(decoder.facts, orders.AccrualFact{Path: path, Kind: "OBJECT", Presence: orders.PresenceValue, Size: &size})
	keys := make([]string, 0, len(node.fields))
	for key := range node.fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if err := decoder.node(fields[key], accrualPath(path, key), node.fields[key]); err != nil {
			return err
		}
	}
	for key, value := range fields {
		if _, known := node.fields[key]; known {
			continue
		}
		if err := decoder.unknownValue(value, accrualPath(path, accrualSafeName(key))); err != nil {
			return err
		}
	}
	return nil
}

func (decoder *accrualDecoder) array(raw json.RawMessage, path string, node accrualNode) error {
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || rows == nil || len(rows) > 10000 {
		return orders.ErrSchemaDrift
	}
	size := len(rows)
	presence := orders.PresenceValue
	if size == 0 {
		presence = orders.PresenceEmpty
	}
	decoder.facts = append(decoder.facts, orders.AccrualFact{Path: path, Kind: "ARRAY", Presence: presence, Size: &size})
	for i, row := range rows {
		index := i
		if path == "accruals" {
			index += decoder.offset
		}
		if err := decoder.node(row, path+"["+strconv.Itoa(index)+"]", *node.item); err != nil {
			return err
		}
	}
	return nil
}

func accrualPath(parent, key string) string {
	if parent == "$" {
		return key
	}
	return parent + "." + key
}

func accrualSafeName(key string) string {
	if financialComponentName.MatchString(key) {
		return key
	}
	hash := sha256.Sum256([]byte(key))
	return "field_" + hex.EncodeToString(hash[:])
}

func (decoder *accrualDecoder) unknownValue(raw json.RawMessage, path string) error {
	value, err := unknownRealizationValue(raw)
	if err != nil {
		return err
	}
	decoder.unknown = append(decoder.unknown, orders.AccrualUnknownField{Path: path, Kind: value.Kind, Value: value.Value, ValueDigest: value.ValueDigest})
	return nil
}
