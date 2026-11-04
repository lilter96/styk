package orders

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRelatedPostingsValidateDistinctPresence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		group RelatedPostingGroup
		valid bool
	}{
		{"null", RelatedPostingGroup{Presence: PresenceNull}, true},
		{"empty object", RelatedPostingGroup{Presence: PresenceEmpty}, true},
		{"missing member", RelatedPostingGroup{Presence: PresenceValue}, true},
		{"null member", RelatedPostingGroup{Presence: PresenceValue, PostingNumbers: Field[[]string]{Presence: PresenceNull}}, true},
		{"empty list", RelatedPostingGroup{Presence: PresenceValue, PostingNumbers: Present([]string{})}, true},
		{"assembly includes current", RelatedPostingGroup{Presence: PresenceValue, PostingNumbers: Present([]string{"current", "sibling"})}, true},
		{"duplicate", RelatedPostingGroup{Presence: PresenceValue, PostingNumbers: Present([]string{"same", "same"})}, false},
		{"invalid id", RelatedPostingGroup{Presence: PresenceValue, PostingNumbers: Present([]string{"\n"})}, false},
		{"long id", RelatedPostingGroup{Presence: PresenceValue, PostingNumbers: Present([]string{strings.Repeat("x", 201)})}, false},
		{"unknown presence", RelatedPostingGroup{Presence: "future"}, false},
		{"null with members", RelatedPostingGroup{Presence: PresenceNull, PostingNumbers: Present([]string{"sibling"})}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (tc.group.Validate() == nil) != tc.valid {
				t.Fatal("invalid related group accepted or valid group rejected")
			}
		})
	}
}

func TestRelatedPostingFingerprintStableWithoutInventingAncestry(t *testing.T) {
	t.Parallel()
	posting := Posting{Scope: Scope{Tenant: "tenant", OzonAccount: "ozon", MoyskladAccount: "moy"}, CommercialOrderID: "order", PostingNumber: "current", Lines: []PostingLine{{Identity: LineIdentity{SKU: "sku"}, Quantity: "1"}}, Evidence: EvidenceRevision{Revision: "revision", Origin: OriginOzon, ObservedAt: time.Now().UTC()}}
	original, err := json.Marshal(posting)
	if err != nil || strings.Contains(string(original), "related_postings") {
		t.Fatal("new optional group rewrote historical representation", err)
	}
	numbers := []string{"sibling-z", "current", "sibling-a"}
	posting.RelatedPostings = &RelatedPostingGroup{Presence: PresenceValue, PostingNumbers: Present(numbers)}
	before, err := PostingFingerprint(posting)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(numbers, []string{"sibling-z", "current", "sibling-a"}) {
		t.Fatal("fingerprinting mutated source order")
	}
	canonical := posting.RelatedPostings.Canonical()
	posting.RelatedPostings = canonical
	after, err := PostingFingerprint(posting)
	if err != nil || after != before {
		t.Fatal("assembly order changed fingerprint", err)
	}
	if posting.ParentPostingNumber.IsKnown() || len(posting.ChildPostingNumbers) != 0 {
		t.Fatal("siblings fabricated parent or children")
	}
	(*canonical.PostingNumbers.Value)[0] = "changed"
	if numbers[0] != "sibling-z" {
		t.Fatal("canonical group aliases original observation")
	}
}
