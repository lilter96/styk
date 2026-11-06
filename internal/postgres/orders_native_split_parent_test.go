package postgres

import "testing"

func TestNativeProviderParentDistinguishesRootFromInvalidEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, raw, parent string
		valid             bool
	}{
		{"missing parent", `{"presence":"missing"}`, "", true},
		{"null parent", `{"presence":"null"}`, "", true},
		{"Ozon root posting empty string", `{"presence":"empty"}`, "", true},
		{"known parent", `{"presence":"value","value":"parent-posting"}`, "parent-posting", true},
		{"invalid presence", `{"presence":"unknown"}`, "", false},
		{"inconsistent empty evidence", `{"presence":"empty","value":"parent-posting"}`, "", false},
		{"missing parent value", `{"presence":"value"}`, "", false},
		{"malformed evidence", `{`, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parent, valid := nativeProviderParent([]byte(tc.raw))
			if parent != tc.parent || valid != tc.valid {
				t.Fatalf("parent=%q valid=%v; want %q %v", parent, valid, tc.parent, tc.valid)
			}
		})
	}
}
