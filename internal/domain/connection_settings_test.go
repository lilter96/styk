package domain

import (
	"strings"
	"testing"
)

func TestConnectionSettingsValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings ConnectionSettings
		valid    bool
	}{
		{"valid unicode", ConnectionSettings{Name: "Основной аккаунт", Description: "Склад в Минске"}, true},
		{"optional description", ConnectionSettings{Name: "Account"}, true},
		{"empty name", ConnectionSettings{}, false},
		{"whitespace", ConnectionSettings{Name: " Account"}, false},
		{"long unicode name", ConnectionSettings{Name: strings.Repeat("я", 121)}, false},
		{"control characters", ConnectionSettings{Name: "Account", Description: "hidden\x00text"}, false},
		{"long description", ConnectionSettings{Name: "Account", Description: strings.Repeat("я", 1001)}, false},
		{"negative revision", ConnectionSettings{Name: "Account", Revision: -1}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.settings.IsValid() != tc.valid {
				t.Fatalf("validation = %v, want %v", tc.settings.IsValid(), tc.valid)
			}
		})
	}
}
