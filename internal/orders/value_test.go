package orders

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestFieldPresenceValidation(t *testing.T) {
	t.Parallel()
	zero, one := int64(0), int64(1)
	tests := []struct {
		name  string
		field Field[int64]
		valid bool
	}{
		{"zero value is missing", Field[int64]{}, true},
		{"explicit missing", Field[int64]{Presence: PresenceMissing}, true},
		{"null", Field[int64]{Presence: PresenceNull}, true},
		{"empty", Field[int64]{Presence: PresenceEmpty}, true},
		{"numeric zero", Field[int64]{Presence: PresenceZero, Value: &zero}, true},
		{"value", Field[int64]{Presence: PresenceValue, Value: &one}, true},
		{"missing with value", Field[int64]{Presence: PresenceMissing, Value: &zero}, false},
		{"null with value", Field[int64]{Presence: PresenceNull, Value: &one}, false},
		{"known without value", Field[int64]{Presence: PresenceValue}, false},
		{"zero as value", Field[int64]{Presence: PresenceValue, Value: &zero}, false},
		{"nonzero as zero", Field[int64]{Presence: PresenceZero, Value: &one}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.field.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("validation = %v, want valid %t", err, tc.valid)
			}
		})
	}
}

func TestPresentKeepsTypedZeroAndEmpty(t *testing.T) {
	t.Parallel()
	if field := Present(false); field.Presence != PresenceZero || field.Validate() != nil {
		t.Fatal("false must remain an explicit zero")
	}
	if field := Present(Decimal("0.00")); field.Presence != PresenceZero || field.Validate() != nil {
		t.Fatal("exact decimal zero must remain explicit")
	}
	if field := Present(""); field.Presence != PresenceEmpty || field.Value != nil {
		t.Fatal("observed empty text must remain empty")
	}
	data, err := json.Marshal(Field[string]{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"presence":"missing"}` {
		t.Fatalf("zero field JSON = %s", data)
	}
}

func TestDecimalRejectsApproximateRepresentations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value Decimal
		valid bool
	}{
		{"0", true}, {"0.000", true}, {"9007199254740993.01", true}, {"-0.25", true},
		{"1e2", false}, {"+1", false}, {" 1", false}, {"1 ", false}, {"NaN", false},
		{"01", false}, {"1.", false}, {".1", false}, {"--1", false}, {"", false},
	}
	for _, tc := range tests {
		t.Run(string(tc.value), func(t *testing.T) {
			t.Parallel()
			err := tc.value.Validate()
			if (err == nil) != tc.valid || (err != nil && !errors.Is(err, ErrInvalid)) {
				t.Fatalf("validation = %v, want valid %t", err, tc.valid)
			}
		})
	}
}
