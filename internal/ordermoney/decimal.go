// Package ordermoney projects reviewed order lines using exact decimal amounts.
package ordermoney

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

// Keep the source and result bound consistent with orders.Decimal.Validate.
const decimalTextLimit = 128

var ErrDecimalRange = fmt.Errorf("exact order decimal exceeds its text bound: %w", orders.ErrInvalid)

type exactDecimal struct {
	coefficient *big.Int
	scale       int
}

func parseDecimal(value orders.Decimal) (exactDecimal, error) {
	if err := value.Validate(); err != nil {
		return exactDecimal{}, fmt.Errorf("parse exact order decimal: %w", err)
	}
	text, scale := string(value), 0
	if point := strings.IndexByte(text, '.'); point >= 0 {
		scale = len(text) - point - 1
		text = text[:point] + text[point+1:]
	}
	coefficient, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return exactDecimal{}, orders.ErrInvalid
	}
	return exactDecimal{coefficient: coefficient, scale: scale}, nil
}

func powerOfTen(exponent int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
}

func (value exactDecimal) shifted(places int) exactDecimal {
	result := exactDecimal{coefficient: new(big.Int).Set(value.coefficient), scale: value.scale - places}
	if result.scale < 0 {
		result.coefficient.Mul(result.coefficient, powerOfTen(-result.scale))
		result.scale = 0
	}
	return result
}

func (value exactDecimal) aligned(scale int) *big.Int {
	return new(big.Int).Mul(value.coefficient, powerOfTen(scale-value.scale))
}

func (value exactDecimal) plus(other exactDecimal) exactDecimal {
	scale := max(value.scale, other.scale)
	return exactDecimal{coefficient: new(big.Int).Add(value.aligned(scale), other.aligned(scale)), scale: scale}
}

func (value exactDecimal) times(other exactDecimal) exactDecimal {
	return exactDecimal{coefficient: new(big.Int).Mul(value.coefficient, other.coefficient), scale: value.scale + other.scale}
}

func (value exactDecimal) equal(other exactDecimal) bool {
	scale := max(value.scale, other.scale)
	return value.aligned(scale).Cmp(other.aligned(scale)) == 0
}

// rounded uses half-up on the aggregate, never on unit prices or each line.
func (value exactDecimal) rounded(scale int) exactDecimal {
	if value.scale <= scale {
		return value
	}
	divisor := powerOfTen(value.scale - scale)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(value.coefficient, divisor, remainder)
	if new(big.Int).Lsh(new(big.Int).Abs(remainder), 1).Cmp(divisor) >= 0 {
		quotient.Add(quotient, big.NewInt(int64(value.coefficient.Sign())))
	}
	return exactDecimal{coefficient: quotient, scale: scale}
}

func (value exactDecimal) decimal() (orders.Decimal, error) {
	text := new(big.Int).Abs(value.coefficient).String()
	if value.scale > 0 {
		if len(text) <= value.scale {
			text = strings.Repeat("0", value.scale-len(text)+1) + text
		}
		point := len(text) - value.scale
		text = strings.TrimRight(text[:point]+"."+text[point:], "0")
		text = strings.TrimSuffix(text, ".")
	}
	if value.coefficient.Sign() < 0 {
		text = "-" + text
	}
	if len(text) > decimalTextLimit {
		return "", ErrDecimalRange
	}
	return orders.Decimal(text), nil
}
