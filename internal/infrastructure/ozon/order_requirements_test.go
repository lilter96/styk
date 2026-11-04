package ozon

import (
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestReadPackingRequirementsPreservesPresenceAndExactSKUs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		presence   orders.Presence
	}{
		{"missing", `{}`, orders.PresenceMissing}, {"null", `{"requirements":null}`, orders.PresenceNull}, {"empty object", `{"requirements":{}}`, orders.PresenceEmpty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newOrderFields([]byte(tc.body))
			r := decodeOrderRequirements(f)
			if tc.presence == orders.PresenceMissing {
				if r != nil {
					t.Fatal("missing object fabricated")
				}
				return
			}
			if r == nil || r.Presence != tc.presence || *f.err != nil {
				t.Fatal("object presence lost")
			}
		})
	}
	f := newOrderFields([]byte(`{"requirements":{"products_requiring_gtd":[],"products_requiring_country":null,"products_requiring_mandatory_mark":["9007199254740993",123]}}`))
	r := decodeOrderRequirements(f)
	if *f.err != nil || r.Products[orders.RequirementGTD].Presence != orders.PresenceEmpty || r.Products[orders.RequirementCountry].Presence != orders.PresenceNull || !r.Products[orders.RequirementRNPT].IsMissing() {
		t.Fatal("list presence lost")
	}
	if got := *r.Products[orders.RequirementMandatoryMark].Value; len(got) != 2 || got[1] != "9007199254740993" {
		t.Fatal("SKU rounded or identity substituted")
	}
}

func TestReadPackingRequirementsRejectsInvalidAndDetectsUnknownRules(t *testing.T) {
	t.Parallel()
	for _, value := range []string{`["123","123"]`, `["0"]`, `["01"]`, `[-1]`, `[1.5]`, `["9223372036854775808"]`, `[null]`, `"123"`, `[` + strings.Repeat(`"1",`, 1000) + `"2"]`} {
		f := newOrderFields([]byte(`{"requirements":{"products_requiring_gtd":` + value + `}}`))
		decodeOrderRequirements(f)
		if *f.err == nil {
			t.Fatal("invalid requirement SKU list accepted")
		}
	}
	f := newOrderFields([]byte(`{"requirements":{"future_sensitive_requirement":"PRIVATE"}}`))
	r := decodeOrderRequirements(f)
	if *f.err != nil || !r.UnknownFields || len(*f.drift) != 1 || (*f.drift)[0].Path != "$.requirements" {
		t.Fatal("new requirement not quarantined without leaking its name/value")
	}
}
