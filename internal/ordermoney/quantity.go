package ordermoney

import (
	"math/big"

	"github.com/lilter96/styk/internal/orders"
)

// RemainingQuantity is exact movement bookkeeping, not an inventory formula.
// It shares the existing bounded decimal implementation and never rounds.
func RemainingQuantity(total orders.Decimal, consumed ...orders.Decimal) (orders.Decimal, error) {
	remaining, err := parseDecimal(total)
	if err != nil || remaining.coefficient.Sign() < 0 {
		return "", orders.ErrInvalid
	}
	for _, quantity := range consumed {
		value, parseErr := parseDecimal(quantity)
		if parseErr != nil || value.coefficient.Sign() < 0 {
			return "", orders.ErrInvalid
		}
		negative := exactDecimal{coefficient: new(big.Int).Neg(value.coefficient), scale: value.scale}
		remaining = remaining.plus(negative)
		if remaining.coefficient.Sign() < 0 {
			return "", orders.ErrConflict
		}
	}
	return remaining.decimal()
}
