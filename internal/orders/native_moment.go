package orders

import "time"

// MoySklad's general DateTime contract explicitly sets and returns moment with
// seconds and milliseconds zero. Checked 2026-11-04:
// https://raw.githubusercontent.com/moysklad/api-remap-1.2-doc/master/md/_general.md
// SHA256: 5dc61a23b376b7b2ea46ece7ac6f33f56476d2c34fdf152d457cbeb8e0da0c16.
// This is an accounting document date, not the precise physical event timestamp.
func NativeAccountingMoment(value time.Time) time.Time {
	return value.UTC().Truncate(time.Minute)
}

// Historical immutable intents retain their original precision. Compatibility
// allows exact old millisecond values or the provider's exact documented floor,
// never an arbitrary time in the same minute or a neighboring minute.
func nativeDocumentMomentMatches(wanted time.Time, actual Field[time.Time]) bool {
	if wanted.IsZero() || !actual.IsKnown() {
		return false
	}
	return nativeDocumentMomentExact(wanted, *actual.Value) || NativeAccountingMoment(wanted).Equal(actual.Value.UTC())
}

func nativeDocumentMomentExact(wanted, actual time.Time) bool {
	return wanted.UTC().Truncate(time.Millisecond).Equal(actual.UTC().Truncate(time.Millisecond))
}

func NativeDocumentMomentNormalized(command Command, actual NativeDocumentObservation) bool {
	if command.NativeMovement == nil || !actual.Moment.IsKnown() {
		return false
	}
	wanted := command.NativeMovement.Moment
	return !nativeDocumentMomentExact(wanted, *actual.Moment.Value) && NativeAccountingMoment(wanted).Equal(actual.Moment.Value.UTC())
}

// AccountingMomentMatches is only for the documented MoySklad moment and
// deliveryPlannedMoment fields, never source freshness or physical event times.
func AccountingMomentMatches(wanted, actual Field[time.Time]) bool {
	return !wanted.IsKnown() || nativeDocumentMomentMatches(*wanted.Value, actual)
}

func AccountingMomentNormalized(wanted, actual Field[time.Time]) bool {
	return wanted.IsKnown() && actual.IsKnown() && !nativeDocumentMomentExact(*wanted.Value, *actual.Value) && NativeAccountingMoment(*wanted.Value).Equal(actual.Value.UTC())
}
