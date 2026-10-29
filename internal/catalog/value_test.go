package catalog

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestValidateValuePreservesExplicitStates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value Value
		valid bool
	}{
		{"known text", Text("value"), true},
		{"known structured fact", Value{State: StateKnown, Data: json.RawMessage(`{"id":42}`)}, true},
		{"observed empty", Empty(), true},
		{"intentional clear", Clear(), true},
		{"absent", Value{State: StateAbsent}, true},
		{"unsupported", Value{State: StateUnsupported}, true},
		{"unknown", Value{State: StateUnknown}, true},
		{"implicit state", Value{}, false},
		{"known null", Value{State: StateKnown, Data: json.RawMessage(`null`)}, false},
		{"known empty text", Value{State: StateKnown, Data: json.RawMessage(`""`)}, false},
		{"unknown cannot carry data", Value{State: StateUnknown, Data: json.RawMessage(`1`)}, false},
		{"clear cannot carry data", Value{State: StateClear, Data: json.RawMessage(`"hidden"`)}, false},
		{"trailing JSON", Value{State: StateKnown, Data: json.RawMessage(`1 2`)}, false},
		{"malformed JSON", Value{State: StateKnown, Data: json.RawMessage(`{"incomplete"`)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateValue(tc.value)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestFingerprintValueIsStableAcrossObservationMetadata(t *testing.T) {
	t.Parallel()
	left := Value{State: StateKnown, Data: json.RawMessage(`{"b": [2, 3], "a": 1}`)}
	right := Value{State: StateKnown, Data: json.RawMessage(`{"a":1,"b":[2,3]}`),
		Provenance: Provenance{Revision: "later", ObservedAt: time.Now()}}
	if fingerprint(t, left) != fingerprint(t, right) {
		t.Fatal("same fact changed fingerprint after read or JSON key ordering")
	}
	if fingerprint(t, Empty()) == fingerprint(t, Clear()) {
		t.Fatal("observed emptiness and clear intent lost their distinct representation")
	}
	if !EqualValue(Empty(), Clear()) {
		t.Fatal("clear readback must converge to observed empty")
	}
	if EqualValue(Value{}, Value{}) {
		t.Fatal("invalid values must never compare equal")
	}
}

func TestTextKeepsEmptyObservationAndEscapesContent(t *testing.T) {
	t.Parallel()
	if Text("").State != StateEmpty {
		t.Fatal("empty text became a clear request")
	}
	text := "escaped \"text\"\nwith control \x01 and кириллица"
	actual, err := Text(text).StringValue()
	if err != nil || actual != text {
		t.Fatalf("text did not roundtrip: %q, %v", actual, err)
	}
	if _, err := Clear().StringValue(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("clear was interpreted as known text: %v", err)
	}
}

func fingerprint(t *testing.T, value Value) string {
	t.Helper()
	result, err := FingerprintValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
