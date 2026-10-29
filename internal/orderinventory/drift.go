package orderinventory

import (
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

func materialDrift(drift []orders.Drift) bool {
	for _, entry := range drift {
		if strings.HasPrefix(entry.Code, "ORDERING_") || entry.Code == "schema_invalid" || entry.Code == "invalid_json" {
			return true
		}
		if entry.Code == "schema_unverified" || entry.Code == "unknown_key" {
			continue
		}
		if inventoryFieldPath(entry.Path) || entry.Code == "conflicting_scheme" || entry.Code == "fbo_out_of_scope" || entry.Code == "warehouse_unverified" || entry.Code == "scheme_unverified" {
			return true
		}
		if entry.Path == "$" && entry.Code != "unsupported_typed_field" {
			return true
		}
	}
	return false
}

func inventoryFieldPath(path string) bool {
	for _, field := range []string{"quantity", "cancelled_quantity", "posting_number", "order_id", "offer_id", "sku", "product_id", "warehouse", "status", "scheme", "parent_posting", "child_posting"} {
		if strings.Contains(path, field) {
			return true
		}
	}
	return path == "$.result.products" || path == "$.result.lines"
}
