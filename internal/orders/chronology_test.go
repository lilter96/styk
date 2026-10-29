package orders

import (
	"testing"
	"time"
)

func TestEvidenceOrderingPreservesUncertainty(t *testing.T) {
	t.Parallel()
	before, after := time.Unix(100, 0).UTC(), time.Unix(200, 0).UTC()
	previous := EvidenceRevision{Revision: "one", Origin: OriginOzon, ObservedAt: before}
	tests := []struct {
		name string
		old  EvidenceRevision
		next EvidenceRevision
		want EvidenceOrder
	}{
		{"later reception", previous, EvidenceRevision{Revision: "two", Origin: OriginOzon, ObservedAt: after}, EvidenceUnknown},
		{"earlier reception", previous, EvidenceRevision{Revision: "two", Origin: OriginOzon, ObservedAt: before.Add(-time.Hour)}, EvidenceUnknown},
		{"local sequence", previous, EvidenceRevision{Revision: "two", Origin: OriginOzon, ObservedAt: after, LocalSequence: 100}, EvidenceUnknown},
		{"identical fact revision", previous, EvidenceRevision{Revision: "one", Origin: OriginOzon, ObservedAt: after}, EvidenceSame},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CompareEvidence(tc.old, tc.next); got != tc.want {
				t.Fatalf("order = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestProviderTimestampCanOrderFacts(t *testing.T) {
	t.Parallel()
	before, after := time.Unix(100, 0).UTC(), time.Unix(200, 0).UTC()
	previous := EvidenceRevision{Revision: "one", Origin: OriginOzon, ObservedAt: after, ProviderUpdatedAt: Present(before)}
	next := EvidenceRevision{Revision: "two", Origin: OriginOzon, ObservedAt: before, ProviderUpdatedAt: Present(after)}
	if CompareEvidence(previous, next) != EvidenceNewer || CompareEvidence(next, previous) != EvidenceOlder {
		t.Fatal("provider time must order facts independently of reception time")
	}
	next.ProviderUpdatedAt = Present(before)
	if CompareEvidence(previous, next) != EvidenceConflict {
		t.Fatal("different facts at the same provider timestamp must conflict")
	}
	previous.ProviderVersion, next.ProviderVersion = Present("opaque-version"), Present("opaque-version")
	next.ProviderUpdatedAt = Present(after)
	if CompareEvidence(previous, next) != EvidenceConflict {
		t.Fatal("same provider version with changed facts must conflict")
	}
}
