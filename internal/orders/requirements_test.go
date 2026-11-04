package orders

import (
	"testing"
	"time"
)

func TestPackingRequirementsMergePreservesUnknownButAcceptsExplicitResolution(t *testing.T) {
	t.Parallel()
	before := &PackingRequirements{Presence: PresenceValue, Products: map[PackingRequirementKind]Field[[]string]{RequirementMandatoryMark: Present([]string{"9007199254740993"})}}
	for _, next := range []*PackingRequirements{nil, {Presence: PresenceNull}, {Presence: PresenceEmpty}, {Presence: PresenceValue, Products: map[PackingRequirementKind]Field[[]string]{RequirementMandatoryMark: {Presence: PresenceMissing}}}} {
		merged := MergePackingRequirements(before, next)
		if !merged.Products[RequirementMandatoryMark].IsKnown() {
			t.Fatal("missing requirements erased known evidence")
		}
	}
	next := &PackingRequirements{Presence: PresenceValue, Products: map[PackingRequirementKind]Field[[]string]{RequirementMandatoryMark: Present([]string{})}}
	merged := MergePackingRequirements(before, next)
	if merged.Products[RequirementMandatoryMark].Presence != PresenceEmpty {
		t.Fatal("explicitly resolved requirement stayed outstanding")
	}
	if !before.Products[RequirementMandatoryMark].IsKnown() {
		t.Fatal("merge modified historical evidence")
	}
	if got := MergePackingRequirements(nil, &PackingRequirements{Presence: PresenceNull}); got == nil || got.Presence != PresenceNull {
		t.Fatal("first observed null became missing")
	}
}

func TestPackingRequirementFingerprintNormalizesOrderWithoutLosingSKUPrecision(t *testing.T) {
	t.Parallel()
	p := Posting{Scope: Scope{Tenant: "tenant", MoyskladAccount: "moy", OzonAccount: "ozon"}, CommercialOrderID: "order", PostingNumber: "posting",
		Lines: []PostingLine{{Identity: LineIdentity{SKU: "9007199254740993"}, Quantity: "1"}}, Evidence: EvidenceRevision{Revision: "rev", Origin: OriginOzon, ObservedAt: time.Now().UTC()},
		Requirements: &PackingRequirements{Presence: PresenceValue, Products: map[PackingRequirementKind]Field[[]string]{RequirementMandatoryMark: Present([]string{"9007199254740993", "123"})}}}
	first, err := PostingFingerprint(p)
	if err != nil {
		t.Fatal(err)
	}
	next := p
	next.Requirements = p.Requirements.Canonical()
	second, err := PostingFingerprint(next)
	if err != nil || first != second {
		t.Fatal("unordered requirements changed source identity")
	}
	if (*p.Requirements.Products[RequirementMandatoryMark].Value)[0] != "9007199254740993" {
		t.Fatal("fingerprint mutated history")
	}
}
