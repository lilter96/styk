package orderflow

import (
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lilter96/styk/internal/orders"
)

func (s *Service) Countries(ctx context.Context, request orders.CountriesRequest) (orders.Countries, error) {
	if request.Scope.Validate() != nil || request.PostingNumber == "" || utf8.RuneCountInString(request.Search) > 128 || strings.ContainsAny(request.Search, "\x00\r\n") {
		return orders.Countries{}, orders.ErrInvalid
	}
	review, err := s.reviewContextFor(ctx, orders.PreviewRequest{Scope: request.Scope, PostingNumbers: []string{request.PostingNumber}}, false)
	if err != nil {
		return orders.Countries{}, err
	}
	postings, err := selectedPostings(review.Postings, []string{request.PostingNumber})
	if err != nil || len(postings) != 1 {
		return orders.Countries{}, orders.ErrNotFound
	}
	required, err := orders.CountryRequiredSKUs(postings[0])
	if err != nil {
		return orders.Countries{}, err
	}
	countries, err := readCountryDictionary(ctx, s.resolver, request.Scope, review.Credentials.Ozon, request.Search)
	return orders.Countries{Scope: request.Scope, PostingNumber: request.PostingNumber, SourceRevision: postings[0].Evidence.Revision, RequiredSKUs: required, Countries: countries}, err
}
func readCountryDictionary(ctx context.Context, resolver orders.Resolver, scope orders.Scope, revision int64, search string) ([]orders.Country, error) {
	client, err := resolveOrderClient(ctx, resolver, scope, "ozon", revision)
	if err != nil {
		return nil, err
	}
	reader, ok := client.(orders.CountryReader)
	if !ok {
		return nil, orders.ErrBlocked
	}
	return reader.ReadOrderCountries(ctx, scope, search)
}
func (s *Service) validateCountryProposal(ctx context.Context, review orders.ReviewContext, proposal orders.ActionProposal) []orders.Validation {
	if proposal.Action != orders.ActionCountrySet {
		return nil
	}
	if !orders.CountryISOValid(proposal.Parameters.CountryISOCode) || !proposal.Parameters.CountryConfirmed {
		return []orders.Validation{{Field: "country_iso_code", Code: "country_factual_input_required"}}
	}
	countries, err := readCountryDictionary(ctx, s.resolver, review.Config.Scope, review.Credentials.Ozon, "")
	if err != nil || !slices.ContainsFunc(countries, func(country orders.Country) bool { return country.ISOCode == proposal.Parameters.CountryISOCode }) {
		return []orders.Validation{{Field: "country_iso_code", Code: "country_not_in_current_provider_dictionary"}}
	}
	return nil
}
