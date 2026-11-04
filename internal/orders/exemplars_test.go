package orders

import (
	"errors"
	"testing"
)

func exemplarFixture() (Posting, ExemplarSnapshot) {
	p := testPosting()
	p.Lines = []PostingLine{{Identity: LineIdentity{SKU: "2001"}, Quantity: "2"}}
	r := make(map[PackingRequirementKind]Field[bool])
	for _, k := range PackingRequirementKinds() {
		r[k] = Present(false)
	}
	r[RequirementMandatoryMark] = Present(true)
	unit := ExemplarInput{ID: "9007199254740993", GTD: Present("SYNTHETIC-GTD"), GTDAbsent: Present(false), RNPTAbsent: Present(true), Marks: Present([]ExemplarMark{{Type: "jw_uin", Value: "SYNTHETIC-JW-1"}})}
	second := unit
	second.ID = "9007199254740994"
	second.Marks = Present([]ExemplarMark{{Type: "imei", Value: "SYNTHETIC-IMEI-2"}})
	s := ExemplarSnapshot{PostingNumber: p.PostingNumber, Products: []ExemplarProduct{{SKU: "2001", Quantity: 2, Required: r, Exemplars: []ExemplarInput{unit, second}}}}
	return p, s
}

func TestExemplarOverlayPreservesUnselectedTypesAndProviderIDs(t *testing.T) {
	t.Parallel()
	p, base := exemplarFixture()
	patch := ExemplarSetInput{Products: []ExemplarProductInput{{SKU: "2001", Exemplars: []ExemplarInput{{ID: "9007199254740993", Marks: Present([]ExemplarMark{{Type: "mandatory_mark", Value: "SYNTHETIC\x1dMARK"}})}}}}}
	next, err := ApplyExemplarInput(base, patch, p)
	if err != nil {
		t.Fatal(err)
	}
	u := next.Products[0].Exemplars[0]
	if len(*u.Marks.Value) != 2 || *u.GTD.Value != "SYNTHETIC-GTD" || next.Products[0].Exemplars[1].ID != "9007199254740994" {
		t.Fatal("unmanaged factual values overwritten")
	}
	if len(*base.Products[0].Exemplars[0].Marks.Value) != 1 {
		t.Fatal("historical snapshot mutated")
	}
	patch.Products[0].Exemplars[0].ID = "999"
	if _, err = ApplyExemplarInput(base, patch, p); !errors.Is(err, ErrStale) {
		t.Fatal("invented instance ID accepted")
	}
}

func TestExemplarUnknownAndWeightCannotBeInvented(t *testing.T) {
	t.Parallel()
	p, s := exemplarFixture()
	s.Products[0].Required[RequirementWeight] = Present(true)
	if len(ValidateExemplarSet(s, p)) == 0 {
		t.Fatal("unknown weight unit silently accepted")
	}
	s.Products[0].Required[RequirementMandatoryMark] = Field[bool]{Presence: PresenceMissing}
	if len(ValidateExemplarSet(s, p)) < 2 {
		t.Fatal("missing requirement became false")
	}
	s.Products[0].Exemplars[1].ID = s.Products[0].Exemplars[0].ID
	if s.ValidateFor(p) == nil {
		t.Fatal("one provider unit exposed twice")
	}
}

func TestExemplarNoopAndExplicitTypeClear(t *testing.T) {
	t.Parallel()
	p, s := exemplarFixture()
	patch := ExemplarSetInput{Products: []ExemplarProductInput{{SKU: "2001", Exemplars: []ExemplarInput{{ID: "9007199254740993", Marks: Present([]ExemplarMark{})}}}}}
	next, e := ApplyExemplarInput(s, patch, p)
	if e != nil || !ExemplarValuesEqual(s, next) {
		t.Fatal("empty selection erased all marks")
	}
	patch.Products[0].Exemplars[0].ClearMarkTypes = []string{"jw_uin"}
	next, e = ApplyExemplarInput(s, patch, p)
	if e != nil || next.Products[0].Exemplars[0].Marks.Presence != PresenceEmpty {
		t.Fatal("explicit optional-type clearing lost")
	}
}

func TestExemplarExplicitAbsenceClearsPriorDeclaration(t *testing.T) {
	t.Parallel()
	p, base := exemplarFixture()
	patch := ExemplarSetInput{Products: []ExemplarProductInput{{SKU: "2001", Exemplars: []ExemplarInput{{ID: "9007199254740993", GTDAbsent: Present(true)}}}}}
	next, err := ApplyExemplarInput(base, patch, p)
	if err != nil {
		t.Fatal(err)
	}
	u := next.Products[0].Exemplars[0]
	if u.GTD.Presence != PresenceEmpty || !u.GTDAbsent.IsKnown() || !*u.GTDAbsent.Value || next.ValidateFor(p) != nil {
		t.Fatal("explicit absence retained old declaration")
	}
	patch.Products[0].Exemplars[0].GTD = Present("SYNTHETIC-CONTRADICTORY")
	if _, err = ApplyExemplarInput(base, patch, p); err == nil {
		t.Fatal("contradictory seller declarations accepted")
	}
	patch.Products[0].Exemplars[0] = ExemplarInput{ID: "9007199254740993", RNPT: Present("SYNTHETIC-RNPT")}
	next, err = ApplyExemplarInput(base, patch, p)
	if err != nil || !next.Products[0].Exemplars[0].RNPTAbsent.IsKnown() || *next.Products[0].Exemplars[0].RNPTAbsent.Value {
		t.Fatal("new factual value retained old absence flag")
	}
}
