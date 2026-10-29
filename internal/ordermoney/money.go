package ordermoney

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

var (
	ErrUnsupportedCurrency = fmt.Errorf("order currency has no supported minor-unit scale: %w", orders.ErrBlocked)
	ErrNegativeAmount      = fmt.Errorf("negative order monetary amount: %w", orders.ErrInvalid)
)

// currencyScale is an explicit support record, not a default for every code.
// Only these two-decimal currencies can currently cross this adapter boundary.
func currencyScale(currency string) (int, error) {
	switch currency {
	case "RUB", "BYN", "KZT", "USD", "EUR", "CNY":
		return 2, nil
	default:
		return 0, ErrUnsupportedCurrency
	}
}

// MoneyToMinor converts nonnegative major currency units exactly. MoySklad can
// represent fractional minor units, so this conversion never rounds to integers.
// Both the input and emitted decimal text obey orders.Decimal's 128-byte bound.
func MoneyToMinor(money orders.Money) (json.Number, error) {
	value, scale, err := monetaryDecimal(money)
	if err != nil {
		return "", fmt.Errorf("convert money to minor units: %w", err)
	}
	amount, err := value.shifted(scale).decimal()
	if err != nil {
		return "", fmt.Errorf("format money minor units: %w", err)
	}
	return json.Number(amount), nil
}

// MoneyFromMinor requires explicit currency evidence and preserves fractional
// minor units. Exponents, whitespace and values beyond the domain bound fail.
func MoneyFromMinor(minor json.Number, currency string) (orders.Money, error) {
	value, scale, err := monetaryDecimal(orders.Money{Amount: orders.Decimal(minor), Currency: currency})
	if err != nil {
		return orders.Money{}, fmt.Errorf("convert money from minor units: %w", err)
	}
	amount, err := value.shifted(-scale).decimal()
	if err != nil {
		return orders.Money{}, fmt.Errorf("format money major units: %w", err)
	}
	return orders.Money{Amount: amount, Currency: currency}, nil
}

func monetaryDecimal(money orders.Money) (exactDecimal, int, error) {
	if err := money.Validate(); err != nil {
		return exactDecimal{}, 0, fmt.Errorf("validate order money: %w", err)
	}
	scale, err := currencyScale(money.Currency)
	if err != nil {
		return exactDecimal{}, 0, err
	}
	value, err := parseDecimal(money.Amount)
	if err != nil {
		return exactDecimal{}, 0, err
	}
	if strings.HasPrefix(string(money.Amount), "-") {
		return exactDecimal{}, 0, ErrNegativeAmount
	}
	return value, scale, nil
}
