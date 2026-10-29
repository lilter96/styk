package ozon

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/catalog"
)

// VerifyCatalogFields checks only the approved facts. Provider-generated
// attributes are retained, and unavailable VAT or rewritten image references
// never become evidence of a successful field update.
func (c *CatalogClient) VerifyCatalogFields(command catalog.Command, observed catalog.Product) ([]catalog.FieldError, error) {
	if command.Scope != c.scope || command.Target.Account != (catalog.Account{Provider: catalog.ProviderOzon, ID: c.read.id}) || command.Target.Account != observed.Identity.Account || !catalogPositiveID(observed.Identity.ID) || command.Target.OfferID != observed.Identity.OfferID || (command.Target.ID != "" && command.Target.ID != observed.Identity.ID) {
		return nil, catalog.ErrIdentityMismatch
	}
	problems := make([]catalog.FieldError, 0, len(command.Values)+len(command.Extensions))
	for field, expected := range command.Values {
		if catalogVerifiedField(field, expected, observed) {
			continue
		}
		code := "FIELD_READBACK_DIFFERS"
		if field == catalog.FieldImages {
			code = "IMAGE_REFERENCES_NOT_VERIFIED"
		}
		if actual, ok := observed.Fields[field]; !ok || actual.State == catalog.StateUnknown || actual.State == catalog.StateAbsent || actual.State == catalog.StateUnsupported {
			code = "FIELD_READBACK_UNAVAILABLE"
		}
		problems = append(problems, catalog.FieldError{Field: field, Code: code, Message: "The approved provider fact is not verified by readback"})
	}
	for key, expected := range command.Extensions {
		actual, ok := observed.Extensions[key]
		if key == "vat" && ok && catalogDecimalEqual(expected, actual) {
			continue
		}
		code := "EXTENSION_READBACK_UNAVAILABLE"
		if key == "vat" {
			code = "VAT_READBACK_UNAVAILABLE"
			if actual.State == catalog.StateKnown {
				code = "VAT_READBACK_DIFFERS"
			}
		}
		problems = append(problems, catalog.FieldError{Code: code, Message: "The approved extension is not verified by readback"})
	}
	sort.Slice(problems, func(i, j int) bool {
		return string(problems[i].Field)+problems[i].Code < string(problems[j].Field)+problems[j].Code
	})
	return problems, nil
}

func catalogVerifiedField(field catalog.Field, expected catalog.Value, observed catalog.Product) bool {
	actual, ok := observed.Fields[field]
	if field == catalog.FieldAttributes {
		return catalogAttributeSubset(expected, actual, observed)
	}
	if !ok {
		return false
	}
	if field == catalog.FieldPrice {
		return catalogDecimalEqual(expected, actual)
	}
	return catalog.EqualValue(expected, actual)
}

func catalogDecimalEqual(left, right catalog.Value) bool {
	a, aerr := left.StringValue()
	b, berr := right.StringValue()
	if aerr != nil || berr != nil || !catalogDecimal.MatchString(a) || !catalogDecimal.MatchString(b) {
		return false
	}
	return catalogDecimalCanonical(a) == catalogDecimalCanonical(b)
}

func catalogDecimalCanonical(value string) string {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) == 1 {
		return parts[0]
	}
	fraction := strings.TrimRight(parts[1], "0")
	if fraction == "" {
		return parts[0]
	}
	return parts[0] + "." + fraction
}

func catalogAttributeSubset(expected, actual catalog.Value, observed catalog.Product) bool {
	var selected, reported []CatalogAttribute
	if expected.State != catalog.StateKnown || decodeCatalogFacts(expected.Data, &selected) != nil {
		return false
	}
	if len(selected) == 0 {
		return true
	}
	if actual.State == catalog.StateKnown {
		if json.Unmarshal(actual.Data, &reported) != nil {
			return false
		}
	} else if actual.State != catalog.StateEmpty {
		return false
	}
	byKey := make(map[string]CatalogAttribute, len(reported))
	for _, attribute := range reported {
		key := strconv.FormatInt(attribute.ID, 10) + ":" + strconv.FormatInt(attribute.ComplexID, 10)
		if _, duplicate := byKey[key]; duplicate {
			return false
		}
		byKey[key] = attribute
	}
	for _, attribute := range selected {
		if attribute.ID == 4180 || attribute.ID == 4191 {
			if !catalogNativeAttributeMatches(attribute, observed) {
				return false
			}
			continue
		}
		key := strconv.FormatInt(attribute.ID, 10) + ":" + strconv.FormatInt(attribute.ComplexID, 10)
		reported, ok := byKey[key]
		if !ok || !catalogAttributeValuesEqual(attribute.Values, reported.Values) {
			return false
		}
	}
	return true
}

func catalogNativeAttributeMatches(attribute CatalogAttribute, observed catalog.Product) bool {
	if attribute.ComplexID != 0 || len(attribute.Values) != 1 || attribute.Values[0].DictionaryValueID != 0 {
		return false
	}
	field := catalog.FieldName
	if attribute.ID == 4191 {
		field = catalog.FieldDescription
	}
	return catalog.EqualValue(catalog.Text(attribute.Values[0].Value), observed.Fields[field])
}

func catalogAttributeValuesEqual(left, right []AttributeValue) bool {
	if len(left) != len(right) {
		return false
	}
	for index, value := range left {
		if value.DictionaryValueID != right[index].DictionaryValueID {
			return false
		}
		if value.DictionaryValueID == 0 && value.Value != right[index].Value {
			return false
		}
	}
	return true
}
