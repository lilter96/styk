package orders

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestHistoricalTargetReferenceDoesNotAcquireInventedProviderVersion(t *testing.T) {
	t.Parallel()
	legacy := TargetObservation{Scope: Scope{Tenant: "merchant", MoyskladAccount: "moy", OzonAccount: "ozon"}, Correlation: Correlation{PostingNumber: "posting"}, Revision: "original", Complete: true}
	before, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, []byte("provider_updated_at")) {
		t.Fatal("absent historical provider timestamp changed serialized reference")
	}
	var decoded TargetObservation
	if err = json.Unmarshal(before, &decoded); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(decoded)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("historical reference hash would change on decode", err)
	}
	decoded.ProviderUpdatedAt = Present("2026-11-04 15:00:00.000")
	observed, err := json.Marshal(decoded)
	if err != nil || !bytes.Contains(observed, []byte("provider_updated_at")) {
		t.Fatal("actually observed provider version lost", err)
	}
}
