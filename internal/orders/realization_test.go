package orders

import (
	"errors"
	"testing"
	"time"
)

func TestRealizationPeriodRequiresSupportedClosedMonth(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		period   RealizationPeriod
		expected error
	}{
		{"first supported month", RealizationPeriod{Year: 2023, Month: 8}, nil},
		{"previous month", RealizationPeriod{Year: 2026, Month: 10}, nil},
		{"older unavailable history", RealizationPeriod{Year: 2023, Month: 7}, ErrInvalid},
		{"invalid month", RealizationPeriod{Year: 2026, Month: 13}, ErrInvalid},
		{"open month", RealizationPeriod{Year: 2026, Month: 11}, ErrBlocked},
		{"future month", RealizationPeriod{Year: 2027, Month: 1}, ErrBlocked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.period.Validate(now); !errors.Is(err, tc.expected) {
				t.Fatalf("period error = %v, want %v", err, tc.expected)
			}
		})
	}
}
