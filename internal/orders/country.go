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
	r := posting.Requirements
	if r == nil || r.Validate() != nil || r.UnknownFields || r.Presence != PresenceValue {
		return nil, ErrBlocked
	}
	result := make([]string, 0)
	for _, kind := range []PackingRequirementKind{RequirementCountry, RequirementChangeCountry} {
		field := r.Products[kind]
		if field.Presence == PresenceEmpty {
			continue
		}
		if !field.IsKnown() {
			return nil, ErrBlocked
		}
		for _, sku := range *field.Value {
			if !slices.ContainsFunc(posting.Lines, func(line PostingLine) bool { return line.Identity.SKU == sku }) {
				return nil, ErrStale
			}
			if !slices.Contains(result, sku) {
				result = append(result, sku)
			}
		}
	}
	slices.Sort(result)
	return result, nil
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
	required, err := CountryRequiredSKUs(posting)
	return err == nil && exemplarID(sku) && slices.ContainsFunc(posting.Lines, func(line PostingLine) bool { return line.Identity.SKU == sku }) && !slices.Contains(required, sku)
}
