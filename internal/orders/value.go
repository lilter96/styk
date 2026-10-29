package orders

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func (field Field[T]) MarshalJSON() ([]byte, error) {
	type wireField Field[T]
	if field.IsMissing() {
		field.Presence = PresenceMissing
	}
	return json.Marshal(wireField(field))
}

// Present records explicit emptiness and numeric/boolean zero separately.
func Present[T any](value T) Field[T] {
	presence := valuePresence(value)
	if presence == PresenceEmpty {
		return Field[T]{Presence: presence}
	}
	return Field[T]{Presence: presence, Value: &value}
}

func (field Field[T]) IsMissing() bool {
	return field.Presence == "" || field.Presence == PresenceMissing
}

func (field Field[T]) IsKnown() bool {
	return field.Presence == PresenceValue || field.Presence == PresenceZero
}

func (field Field[T]) IsSpecified() bool {
	return !field.IsMissing()
}

// Validate checks the typed representation, not provider support for a field.
func (field Field[T]) Validate() error {
	switch field.Presence {
	case "", PresenceMissing, PresenceNull, PresenceEmpty:
		if field.Value != nil {
			return ErrInvalid
		}
	case PresenceValue, PresenceZero:
		if field.Value == nil || valuePresence(*field.Value) != field.Presence {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func valuePresence(value any) Presence {
	switch typed := value.(type) {
	case Decimal:
		if typed.Validate() == nil && decimalZero(typed) {
			return PresenceZero
		}
	case Money:
		if typed.Amount != "" && decimalZero(typed.Amount) {
			return PresenceZero
		}
	}
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return PresenceZero
	}
	if emptyValue(reflected) {
		return PresenceEmpty
	}
	if reflected.IsZero() {
		return PresenceZero
	}
	return PresenceValue
}

func emptyValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		return value.Len() == 0
	default:
		return false
	}
}

// Validate accepts exact bounded base-ten text without exponent or whitespace.
// Amount conversions belong to ordermoney; this package does no money arithmetic.
func (decimal Decimal) Validate() error {
	text := string(decimal)
	if len(text) == 0 || len(text) > 128 {
		return ErrInvalid
	}
	if text[0] == '-' {
		text = text[1:]
	}
	parts := strings.Split(text, ".")
	if len(parts) > 2 || !decimalDigits(parts[0]) {
		return ErrInvalid
	}
	if len(parts[0]) > 1 && parts[0][0] == '0' {
		return ErrInvalid
	}
	if len(parts) == 2 && !decimalDigits(parts[1]) {
		return ErrInvalid
	}
	return nil
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func decimalZero(value Decimal) bool {
	for _, char := range value {
		if char != '0' && char != '.' && char != '-' {
			return false
		}
	}
	return value != "" && value != "-" && value != "."
}

func positiveDecimal(value Decimal) bool {
	return value.Validate() == nil && value[0] != '-' && !decimalZero(value)
}

func nonnegativeDecimal(value Decimal) bool {
	return value.Validate() == nil && value[0] != '-'
}

func (money Money) Validate() error {
	if err := money.Amount.Validate(); err != nil {
		return fmt.Errorf("validate exact money amount: %w", err)
	}
	if len(money.Currency) != 3 {
		return ErrInvalid
	}
	for _, char := range money.Currency {
		if char < 'A' || char > 'Z' {
			return ErrInvalid
		}
	}
	return nil
}
