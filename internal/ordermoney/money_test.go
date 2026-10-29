package ordermoney

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestMoneyToMinorPreservesExactDecimal(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, amount, currency, want string }{
		{"explicit zero", "0.00", "RUB", "0"},
		{"whole minor", "123.45", "BYN", "12345"},
		{"fractional minor", "1.23456789", "USD", "123.456789"},
		{"beyond float integer", "9007199254740993.125", "EUR", "900719925474099312.5"},
		{"beyond int64", "123456789012345678901234567890.123456789", "KZT", "12345678901234567890123456789012.3456789"},
		{"explicit CNY scale", "0.00001", "CNY", "0.001"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			minor, err := MoneyToMinor(orders.Money{Amount: orders.Decimal(tc.amount), Currency: tc.currency})
			if err != nil || string(minor) != tc.want {
				t.Fatalf("minor = %q, %v; want %q", minor, err, tc.want)
			}
			encoded, err := json.Marshal(struct {
				Price json.Number `json:"price"`
			}{Price: minor})
			if err != nil || string(encoded) != `{"price":`+tc.want+`}` {
				t.Fatalf("exact numeric JSON = %s, %v", encoded, err)
			}
		})
	}
}

func TestMoneyFromMinorPreservesExactDecimal(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, minor, want string }{
		{"zero", "0", "0"},
		{"hundredth", "1", "0.01"},
		{"fractional minor", "123.456789", "1.23456789"},
		{"beyond int64", "12345678901234567890123456789012.3456789", "123456789012345678901234567890.123456789"},
		{"normalizes source zeros", "12300.000", "123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			money, err := MoneyFromMinor(json.Number(tc.minor), "RUB")
			if err != nil || money.Amount != orders.Decimal(tc.want) || money.Currency != "RUB" {
				t.Fatalf("major = %#v, %v; want %q RUB", money, err, tc.want)
			}
		})
	}
}

func TestMoneyConversionsRejectUnreviewedRepresentations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, amount, currency string
		want                   error
	}{
		{"negative", "-1.25", "RUB", ErrNegativeAmount},
		{"negative zero", "-0", "RUB", ErrNegativeAmount},
		{"currency missing", "1", "", orders.ErrInvalid},
		{"currency lower case", "1", "rub", orders.ErrInvalid},
		{"unknown currency", "1", "ABC", ErrUnsupportedCurrency},
		{"zero-decimal currency", "1", "JPY", ErrUnsupportedCurrency},
		{"three-decimal currency", "1", "BHD", ErrUnsupportedCurrency},
		{"exponent", "1e2", "RUB", orders.ErrInvalid},
		{"whitespace", " 1", "RUB", orders.ErrInvalid},
		{"leading zero", "01", "RUB", orders.ErrInvalid},
		{"missing whole part", ".5", "RUB", orders.ErrInvalid},
		{"source exceeds bound", strings.Repeat("9", 129), "RUB", orders.ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := MoneyToMinor(orders.Money{Amount: orders.Decimal(tc.amount), Currency: tc.currency}); !errors.Is(err, tc.want) {
				t.Fatalf("to minor error = %v; want %v", err, tc.want)
			}
			if _, err := MoneyFromMinor(json.Number(tc.amount), tc.currency); !errors.Is(err, tc.want) {
				t.Fatalf("from minor error = %v; want %v", err, tc.want)
			}
		})
	}
}

func TestMoneyConversionChecksResultBoundWithoutIntegerOverflow(t *testing.T) {
	t.Parallel()
	if _, err := MoneyToMinor(orders.Money{Amount: orders.Decimal(strings.Repeat("9", 128)), Currency: "RUB"}); !errors.Is(err, ErrDecimalRange) {
		t.Fatalf("oversize minor result error = %v", err)
	}
	if _, err := MoneyFromMinor(json.Number(strings.Repeat("9", 128)), "RUB"); !errors.Is(err, ErrDecimalRange) {
		t.Fatalf("oversize major result error = %v", err)
	}
	if _, err := MoneyFromMinor(json.Number("0."+strings.Repeat("0", 125)+"1"), "RUB"); !errors.Is(err, ErrDecimalRange) {
		t.Fatalf("oversize fractional major result error = %v", err)
	}
	minor, err := MoneyToMinor(orders.Money{Amount: orders.Decimal(strings.Repeat("9", 126) + ".1"), Currency: "RUB"})
	if err != nil || len(minor) != 128 {
		t.Fatalf("boundary exact minor = %q, %v", minor, err)
	}
}
