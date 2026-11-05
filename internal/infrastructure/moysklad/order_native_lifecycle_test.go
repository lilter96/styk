package moysklad

import (
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestNativeBasisLegacyMissingDeletionDoesNotEraseExplicitFacts(t *testing.T) {
	t.Parallel()
	before := orders.NativeDocumentObservation{Revision: "unchanged", Returns: []orders.NativeReturnEvidence{{Revision: "return-unchanged"}}}
	after := before
	after.Returns = append([]orders.NativeReturnEvidence(nil), before.Returns...)
	after.SourceDeleted.Presence = orders.PresenceMissing
	after.Returns[0].SourceDeleted.Presence = orders.PresenceMissing
	if !sameNativeBasis(before, after) || after.SourceDeleted.Presence != orders.PresenceMissing || after.Returns[0].SourceDeleted.Presence != orders.PresenceMissing {
		t.Fatal("legacy missing evidence changed or normalization mutated input")
	}
	after.SourceDeleted.Presence = orders.PresenceNull
	if sameNativeBasis(before, after) {
		t.Fatal("explicit null erased to missing")
	}
	after.SourceDeleted = orders.Present("")
	if sameNativeBasis(before, after) {
		t.Fatal("explicit empty erased to missing")
	}
	after.SourceDeleted = orders.Field[string]{}
	after.Returns[0].SourceDeleted = orders.Present("2026-11-05 12:00:00.000")
	if sameNativeBasis(before, after) {
		t.Fatal("deleted return erased from basis")
	}
	after.Returns[0].SourceDeleted = orders.Field[string]{}
	after.Revision = "changed"
	if sameNativeBasis(before, after) {
		t.Fatal("provider revision change normalized away")
	}
}
