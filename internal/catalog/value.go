package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var ErrInvalidValue = errors.New("invalid catalog field value")

// ValidateValue checks representation, independently of a provider's capabilities.
func ValidateValue(value Value) error {
	if value.State != StateKnown {
		if !value.State.valid() || len(value.Data) != 0 {
			return ErrInvalidValue
		}
		return nil
	}
	data, err := canonicalJSON(value.Data)
	if err != nil {
		return fmt.Errorf("validate known field: %w", err)
	}
	if bytes.Equal(data, []byte("null")) || bytes.Equal(data, []byte(`""`)) {
		return ErrInvalidValue
	}
	return nil
}

func (state FieldState) valid() bool {
	switch state {
	case StateAbsent, StateKnown, StateEmpty, StateClear, StateUnsupported, StateUnknown:
		return true
	default:
		return false
	}
}

// StringValue returns known text without interpreting unknown or empty states.
func (value Value) StringValue() (string, error) {
	if value.State != StateKnown {
		return "", ErrInvalidValue
	}
	var result string
	if err := json.Unmarshal(value.Data, &result); err != nil {
		return "", fmt.Errorf("decode catalog text: %w", err)
	}
	if result == "" {
		return "", ErrInvalidValue
	}
	return result, nil
}

// FingerprintValue excludes observation time and provenance, which do not change a fact.
func FingerprintValue(value Value) (string, error) {
	if err := ValidateValue(value); err != nil {
		return "", err
	}
	data := []byte(nil)
	if value.State == StateKnown {
		var err error
		data, err = canonicalJSON(value.Data)
		if err != nil {
			return "", err
		}
	}
	return digest(append(append([]byte(value.State), 0), data...)), nil
}

// EqualValue compares facts. An applied clear is observed as EMPTY on readback.
func EqualValue(left, right Value) bool {
	if isCleared(left.State) && isCleared(right.State) {
		return ValidateValue(left) == nil && ValidateValue(right) == nil
	}
	a, err := FingerprintValue(left)
	if err != nil {
		return false
	}
	b, err := FingerprintValue(right)
	return err == nil && a == b
}

func isCleared(state FieldState) bool {
	return state == StateClear || state == StateEmpty
}

func canonicalJSON(data []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode catalog JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidValue
	}
	result, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode catalog JSON: %w", err)
	}
	return result, nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
