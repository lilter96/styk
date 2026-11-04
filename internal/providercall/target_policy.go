package providercall

import "time"

// StockTargetCooldown derives the existing Ozon single-product/warehouse stock
// policy from exact persisted attempt metadata, never a caller supplied delay.
// Store implementations must first validate the attempt against its permit.
func StockTargetCooldown(attempt Attempt) time.Duration {
	const operation = "v2/products/stocks"
	if attempt.Scope.Provider != "ozon" || !attempt.IsWrite || attempt.TargetKey == "" || attempt.Operation != digest(operation) || attempt.Method != digest("POST\x00"+operation) {
		return 0
	}
	return 120 * time.Second
}
