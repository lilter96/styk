package orders

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestProviderWeightAndGroupingArePreservedButNeverMerchantEdited(t *testing.T) {
	t.Parallel()
	posting, base := exemplarFixture()
	base.MultiBoxQty, posting.MultiBoxQty = Present(int64(2)), Present(int64(2))
	base.Products[0].Exemplars[0].Weight = Present(Decimal("0.000"))
	patch := ExemplarSetInput{Products: []ExemplarProductInput{{SKU: "2001", Exemplars: []ExemplarInput{{ID: base.Products[0].Exemplars[0].ID, Marks: Present([]ExemplarMark{{Type: "mandatory_mark", Value: "SYNTHETIC-MARK"}})}}}}}
	next, err := ApplyExemplarInput(base, patch, posting)
	if err != nil || !next.MultiBoxQty.IsKnown() || *next.MultiBoxQty.Value != 2 || !next.Products[0].Exemplars[0].Weight.IsKnown() || *next.Products[0].Exemplars[0].Weight.Value != "0.000" {
		t.Fatal("native observed values erased or converted", err)
	}
	patch.Products[0].Exemplars[0].Weight = Present(Decimal("0.000"))
	if _, err = ApplyExemplarInput(base, patch, posting); !errors.Is(err, ErrBlocked) {
		t.Fatal("merchant supplied even unchanged provider-owned weight")
	}
	patch.Products[0].Exemplars[0].Weight = Field[Decimal]{}
	patch.MultiBoxQty = Present(int64(2))
	if _, err = ApplyExemplarInput(base, patch, posting); !errors.Is(err, ErrBlocked) {
		t.Fatal("merchant supplied provider-owned box count")
	}
}

func TestFreshWeightCannotBeMissingNullOrInventedZero(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		actual Field[Decimal]
		want   bool
	}{
		{"same exact value", Present(Decimal("1.250")), true}, {"missing", Field[Decimal]{}, false}, {"null", Field[Decimal]{Presence: PresenceNull}, false}, {"zero", Present(Decimal("0")), false}, {"changed", Present(Decimal("1250")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if observedExemplarWeightEqual(Present(Decimal("1.25")), tc.actual) != tc.want {
				t.Fatal("fresh weight evidence was fabricated or converted")
			}
		})
	}
}

func TestRequiredWeightNeedsExistingPositiveFactualValue(t *testing.T) {
	t.Parallel()
	posting, snapshot := exemplarFixture()
	snapshot.Products[0].Required[RequirementMandatoryMark] = Present(false)
	snapshot.Products[0].Required[RequirementWeight] = Present(true)
	for i := range snapshot.Products[0].Exemplars {
		snapshot.Products[0].Exemplars[i].Weight = Present(Decimal("1.25"))
	}
	if got := ValidateExemplarSet(snapshot, posting); len(got) != 0 {
		t.Fatalf("preserving already observed positive weight blocked: %+v", got)
	}
	snapshot.Products[0].Exemplars[0].Weight = Present(Decimal("0"))
	if got := ValidateExemplarSet(snapshot, posting); len(got) != 1 || got[0].Code != "exemplar_weight_observed_value_required" {
		t.Fatalf("zero placeholder satisfied required weight: %+v", got)
	}
}

func TestGroupingUsesSeparatePostingDetailAndInvalidatesPackingTarget(t *testing.T) {
	t.Parallel()
	posting, snapshot := exemplarFixture()
	posting.WarehouseID, posting.DeliveryMethodID = Present("warehouse"), Present("method")
	before, err := PackingTargetFingerprint(posting)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.MultiBoxQty = Present(int64(2))
	if got := exemplarBoxPreservationValidation(snapshot, posting); len(got) != 1 {
		t.Fatal("missing fresh detail count inferred zero")
	}
	posting.MultiBoxQty = Present(int64(2))
	if got := exemplarBoxPreservationValidation(snapshot, posting); len(got) != 0 {
		t.Fatal("same provider grouping blocked")
	}
	after, err := PackingTargetFingerprint(posting)
	if err != nil || after == before {
		t.Fatal("grouping change did not invalidate packing review")
	}
	posting.MultiBoxQty = Present(int64(3))
	if got := exemplarBoxPreservationValidation(snapshot, posting); len(got) != 1 || got[0].Code != "exemplar_multibox_changed" {
		t.Fatal("changed grouping was overwritten")
	}
}

func TestLegacyExemplarJSONDoesNotAcquireUnobservedWeightOrGrouping(t *testing.T) {
	t.Parallel()
	posting, snapshot := exemplarFixture()
	raw, err := json.Marshal(snapshot.Products[0].Exemplars[0])
	if err != nil || strings.Contains(string(raw), `"weight"`) || strings.Contains(string(raw), `"multi_box_qty"`) {
		t.Fatal("missing provider fields changed legacy encrypted snapshot JSON")
	}
	raw, err = json.Marshal(posting)
	if err != nil || strings.Contains(string(raw), `"multi_box_qty"`) {
		t.Fatal("absent grouping altered legacy posting canonical JSON")
	}
}

func TestMissingFreshWeightHasPrecisePendingDiagnostic(t *testing.T) {
	t.Parallel()
	_, snapshot := exemplarFixture()
	snapshot.Products[0].Exemplars[0].Weight = Present(Decimal("0"))
	actual := ExemplarStatus{PostingNumber: snapshot.PostingNumber, Products: []ExemplarStatusProduct{{SKU: "2001", Exemplars: snapshot.Products[0].Exemplars}}}
	actual.Products[0].Exemplars = append([]ExemplarInput(nil), actual.Products[0].Exemplars...)
	actual.Products[0].Exemplars[0].Weight = Field[Decimal]{}
	if code := ExemplarWeightReadbackCode(snapshot, actual); code != "EXEMPLAR_WEIGHT_FRESH_READBACK_REQUIRED" {
		t.Fatal("missing weight was reported as fresh value")
	}
}
