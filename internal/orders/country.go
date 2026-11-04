package orders

import (
	"context"
	"slices"
	"time"
)

const ActionCountrySet Action = "posting_country_set"

type Country struct {
	Name    string `json:"name"`
	ISOCode string `json:"country_iso_code"`
}
type CountriesRequest struct {
	Scope                 Scope
	PostingNumber, Search string
}
type Countries struct {
	Scope          Scope     `json:"scope"`
	PostingNumber  string    `json:"posting_number"`
	SourceRevision string    `json:"source_revision"`
	RequiredSKUs   []string  `json:"required_skus"`
	Countries      []Country `json:"countries"`
}
type CountryReader interface {
	ReadOrderCountries(context.Context, Scope, string) ([]Country, error)
}

// CountryEvidence confirms the requested operation's acknowledged target and
// subsequent requirement state. Posting APIs do not return the stored ISO value.
type CountryEvidence struct {
	Scope                Scope       `json:"scope"`
	SKU                  string      `json:"sku"`
	GTDNeeded            Field[bool] `json:"is_gtd_needed"`
	Meaning              string      `json:"meaning"`
	TargetFingerprint    string      `json:"target_fingerprint"`
	RequirementResolved  bool        `json:"requirement_resolved"`
	CountryValueObserved bool        `json:"country_value_observed"`
	CheckedAt            time.Time   `json:"checked_at"`
}

func CountryISOValid(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}
func CountryRequiredSKUs(posting Posting) ([]string, error) {
	values, complete, err := observedCountryRequirements(posting)
	if err != nil {
		return nil, err
	}
	if !complete && len(values) == 0 {
		return nil, ErrBlocked
	}
	return values, nil
}
func observedCountryRequirements(posting Posting) ([]string, bool, error) {
	r := posting.Requirements
	if r == nil || r.Validate() != nil || r.UnknownFields || r.Presence != PresenceValue {
		return nil, false, ErrBlocked
	}
	result := make([]string, 0)
	complete := true
	for _, kind := range []PackingRequirementKind{RequirementCountry, RequirementChangeCountry} {
		field := r.Products[kind]
		if field.Presence == PresenceEmpty {
			continue
		}
		if !field.IsKnown() {
			complete = false
			continue
		}
		for _, sku := range *field.Value {
			if !slices.ContainsFunc(posting.Lines, func(line PostingLine) bool { return line.Identity.SKU == sku }) {
				return nil, false, ErrStale
			}
			if !slices.Contains(result, sku) {
				result = append(result, sku)
			}
		}
	}
	slices.Sort(result)
	return result, complete, nil
}
func ValidateCountryProposal(p Posting, input ActionParameters) error {
	required, err := CountryRequiredSKUs(p)
	if err != nil {
		return err
	}
	if !input.CountryConfirmed || !CountryISOValid(input.CountryISOCode) || !exemplarID(input.CountrySKU) || !slices.Contains(required, input.CountrySKU) {
		return ErrInvalid
	}
	return nil
}
func CountryRequirementCleared(posting Posting, sku string) bool {
	required, complete, err := observedCountryRequirements(posting)
	return err == nil && complete && exemplarID(sku) && slices.ContainsFunc(posting.Lines, func(line PostingLine) bool { return line.Identity.SKU == sku }) && !slices.Contains(required, sku)
}
