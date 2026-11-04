package orders

import "strconv"

// NativeTaxPreservesPrice accepts a rate already observed in MoySklad, never an
// Ozon fraction or a guessed seller tax declaration. Included VAT changes the
// tax breakdown, not the gross price carried from the native document.
func NativeTaxPreservesPrice(line NativeMovementLine, enabled, included Field[bool]) bool {
	if line.VAT.Validate() != nil || !line.VAT.IsKnown() || !knownNativeBoolean(line.VATEnabled) {
		return false
	}
	rate, err := strconv.Atoi(normalizedPositionDecimal(*line.VAT.Value))
	if err != nil || rate < 0 || rate > 100 {
		return false
	}
	if rate == 0 {
		return true
	}
	return *line.VATEnabled.Value && knownNativeBoolean(enabled) && *enabled.Value && knownNativeBoolean(included) && *included.Value
}
