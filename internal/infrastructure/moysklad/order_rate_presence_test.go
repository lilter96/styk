package moysklad

import (
	"encoding/json"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestOrderNativeRatePreservesObservedMissingVersusNullAndZero(t *testing.T) {
	simulator, adapter := newOrderSimulator(t, "read")
	tests := []struct {
		name     string
		value    any
		omit     bool
		presence orders.Presence
	}{
		{name: "missing", omit: true, presence: orders.PresenceMissing},
		{name: "null", value: nil, presence: orders.PresenceNull},
		{name: "zero", value: json.Number("0"), presence: orders.PresenceZero},
		{name: "explicit", value: json.Number("3.14"), presence: orders.PresenceValue},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := simulator.document()
			rate, ok := raw["rate"].(map[string]any)
			if !ok {
				t.Fatal("native rate fixture invalid")
			}
			if tc.omit {
				delete(rate, "value")
			} else {
				rate["value"] = tc.value
			}
			body, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			var native orderNative
			if err = json.Unmarshal(body, &native); err != nil {
				t.Fatal(err)
			}
			observed, err := adapter.decodeOrderReferences(native)
			if err != nil {
				t.Fatal(err)
			}
			if observed.RateValue.Presence != tc.presence {
				t.Fatalf("rate presence %s, want %s", observed.RateValue.Presence, tc.presence)
			}
		})
	}
}
