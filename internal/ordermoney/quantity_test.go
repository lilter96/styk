package ordermoney

import (
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestRemainingMovementQuantityUsesExactDecimals(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		total    orders.Decimal
		consumed []orders.Decimal
		want     orders.Decimal
		err      error
	}{
		{"9007199254740993.001", []orders.Decimal{"0.0001", "1"}, "9007199254740992.0009", nil},
		{"5", []orders.Decimal{"2", "3"}, "0", nil},
		{"5", []orders.Decimal{"6"}, "", orders.ErrConflict},
		{"5", []orders.Decimal{"-1"}, "", orders.ErrInvalid},
		{"5", []orders.Decimal{"1e1"}, "", orders.ErrInvalid},
	} {
		got, err := RemainingQuantity(test.total, test.consumed...)
		if got != test.want || !errors.Is(err, test.err) {
			t.Fatalf("remaining=%s err=%v", got, err)
		}
	}
}
