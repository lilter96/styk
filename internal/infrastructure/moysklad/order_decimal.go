package moysklad

import (
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

func orderVAT(value orders.Decimal) error {
	if value.Validate() != nil || strings.HasPrefix(string(value), "-") || strings.Contains(string(value), ".") {
		return orders.ErrInvalid
	}
	vat, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil || vat > 100 {
		return orders.ErrInvalid
	}
	return nil
}
