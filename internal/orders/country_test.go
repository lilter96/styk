package orders

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCountryUsesRequiredSKUAndExplicitFactualInput(t *testing.T) {
	t.Parallel()
	p, _ := exemplarFixture()
	p.Requirements = &PackingRequirements{Presence: PresenceValue, Products: map[PackingRequirementKind]Field[[]string]{RequirementCountry: Present([]string{"2001"}), RequirementChangeCountry: Present([]string{})}}
	for _, kind := range PackingRequirementKinds() {
		if _, ok := p.Requirements.Products[kind]; !ok {
			p.Requirements.Products[kind] = Present([]string{})
		}
	}
	input := ActionParameters{CountrySKU: "2001", CountryISOCode: "NO", CountryConfirmed: true}
	if ValidateCountryProposal(p, input) != nil {
		t.Fatal("exact required SKU and factual country rejected")
	}
	for _, mutate := range []func(*ActionParameters){func(v *ActionParameters) { v.CountrySKU = "1001" }, func(v *ActionParameters) { v.CountryISOCode = "ZZZ" }, func(v *ActionParameters) { v.CountryConfirmed = false }} {
		v := input
		mutate(&v)
		if ValidateCountryProposal(p, v) == nil {
			t.Fatal("unrelated or unconfirmed declaration accepted")
		}
	}
	raw, err := json.Marshal(input.Public())
	if err != nil || strings.Contains(string(raw), `"country_iso_code"`) || !strings.Contains(string(raw), `"country_provided":true`) {
		t.Fatal("factual ISO value not protected")
	}
	p.Requirements.Products[RequirementCountry] = Field[[]string]{Presence: PresenceMissing}
	if CountryRequirementCleared(p, "2001") {
		t.Fatal("missing obligation became cleared")
	}
	p.Requirements.Products[RequirementCountry] = Present([]string{})
	if !CountryRequirementCleared(p, "2001") {
		t.Fatal("explicit cleared requirement not observed")
	}
}
