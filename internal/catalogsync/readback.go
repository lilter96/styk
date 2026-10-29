package catalogsync

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/catalog"
)

// ReadbackMatches verifies selected facts, including every selected extension.
// Ozon may add unselected attributes such as its generated description attribute.
// Selected attribute IDs, complex IDs and value sets must still match exactly.
// The store uses this same pure verifier before accepting a RECONCILED outcome.
func ReadbackMatches(command catalog.Command, observed catalog.Product) bool {
	for field, value := range command.Values {
		actual, ok := observed.Fields[field]
		if !ok || !fieldMatches(command.Target.Account.Provider, field, value, actual, observed) {
			return false
		}
	}
	for extension, value := range command.Extensions {
		actual, ok := observed.Extensions[extension]
		if !ok || !extensionMatches(command.Target.Account.Provider, extension, value, actual) {
			return false
		}
	}
	if command.Parent != nil && (observed.Parent == nil || !command.Parent.SameEntity(*observed.Parent)) {
		return false
	}
	return true
}

func fieldMatches(provider catalog.Provider, field catalog.Field, wanted, actual catalog.Value, observed catalog.Product) bool {
	if provider != catalog.ProviderOzon || wanted.State != catalog.StateKnown {
		return catalog.EqualValue(wanted, actual)
	}
	if field == catalog.FieldAttributes {
		return ozonAttributesMatch(wanted, actual, observed)
	}
	if field == catalog.FieldPrice {
		return decimalFactsEqual(wanted, actual)
	}
	return catalog.EqualValue(wanted, actual)
}

func extensionMatches(provider catalog.Provider, key string, wanted, actual catalog.Value) bool {
	if provider == catalog.ProviderOzon && key == "vat" {
		return decimalFactsEqual(wanted, actual)
	}
	return catalog.EqualValue(wanted, actual)
}

type selectedAttribute struct {
	ID        int64                    `json:"id"`
	ComplexID int64                    `json:"complex_id"`
	Values    []selectedAttributeValue `json:"values"`
}

type selectedAttributeValue struct {
	DictionaryValueID int64   `json:"dictionary_value_id"`
	Value             *string `json:"value"`
}

func ozonAttributesMatch(wanted, actual catalog.Value, product catalog.Product) bool {
	selected, ok := decodeAttributes(wanted)
	if !ok || len(selected) > 500 {
		return false
	}
	observed, ok := observedAttributes(actual)
	if !ok {
		return false
	}
	for id, attribute := range selected {
		current, ok := observed[id]
		if id == 4180 || id == 4191 {
			if !nativeAttributeMatches(attribute, product) {
				return false
			}
			if !ok {
				continue
			}
		}
		if !ok || attribute.ComplexID != current.ComplexID || !attributeValuesMatch(attribute.Values, current.Values) {
			return false
		}
	}
	return true
}

func observedAttributes(actual catalog.Value) (map[int64]selectedAttribute, bool) {
	if actual.State == catalog.StateEmpty && catalog.ValidateValue(actual) == nil {
		return make(map[int64]selectedAttribute), true
	}
	return decodeAttributes(actual)
}

func nativeAttributeMatches(attribute selectedAttribute, product catalog.Product) bool {
	if attribute.ComplexID != 0 || len(attribute.Values) != 1 || attribute.Values[0].DictionaryValueID != 0 || attribute.Values[0].Value == nil {
		return false
	}
	field := catalog.FieldName
	if attribute.ID == 4191 {
		field = catalog.FieldDescription
	}
	return catalog.EqualValue(catalog.Text(*attribute.Values[0].Value), product.Fields[field])
}

func decodeAttributes(value catalog.Value) (map[int64]selectedAttribute, bool) {
	if value.State != catalog.StateKnown || len(value.Data) > 4<<20 || catalog.ValidateValue(value) != nil {
		return nil, false
	}
	if !uniqueJSONKeys(json.NewDecoder(bytes.NewReader(value.Data)), 0) {
		return nil, false
	}
	var attributes []selectedAttribute
	decoder := json.NewDecoder(bytes.NewReader(value.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&attributes); err != nil || len(attributes) > 1000 {
		return nil, false
	}
	result := make(map[int64]selectedAttribute, len(attributes))
	for _, attribute := range attributes {
		if attribute.ID <= 0 || attribute.ComplexID < 0 || !validAttributeValues(attribute.Values) {
			return nil, false
		}
		if _, duplicate := result[attribute.ID]; duplicate {
			return nil, false
		}
		result[attribute.ID] = attribute
	}
	return result, true
}

func uniqueJSONKeys(decoder *json.Decoder, depth int) bool {
	if depth > 8 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return true
	}
	if delimiter == '{' {
		return uniqueObjectKeys(decoder, depth+1)
	}
	if delimiter != '[' {
		return false
	}
	for decoder.More() {
		if !uniqueJSONKeys(decoder, depth+1) {
			return false
		}
	}
	end, err := decoder.Token()
	return err == nil && end == json.Delim(']')
}

func uniqueObjectKeys(decoder *json.Decoder, depth int) bool {
	seen := make(map[string]bool, 4)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return false
		}
		seen[key] = true
		if !uniqueJSONKeys(decoder, depth) {
			return false
		}
	}
	end, err := decoder.Token()
	return err == nil && end == json.Delim('}')
}

func validAttributeValues(values []selectedAttributeValue) bool {
	if len(values) == 0 || len(values) > 100 {
		return false
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value.DictionaryValueID < 0 || (value.DictionaryValueID == 0 && (value.Value == nil || *value.Value == "")) {
			return false
		}
		key := attributeValueKey(value)
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func attributeValueKey(value selectedAttributeValue) string {
	if value.DictionaryValueID != 0 {
		return "dictionary:" + strconv.FormatInt(value.DictionaryValueID, 10)
	}
	if value.Value == nil {
		return ""
	}
	return "text:" + *value.Value
}

func attributeValuesMatch(wanted, actual []selectedAttributeValue) bool {
	if len(wanted) != len(actual) {
		return false
	}
	observed := make(map[string]selectedAttributeValue, len(actual))
	for _, value := range actual {
		observed[attributeValueKey(value)] = value
	}
	for _, selected := range wanted {
		current, ok := observed[attributeValueKey(selected)]
		if !ok || (selected.DictionaryValueID == 0 && selected.Value != nil && (current.Value == nil || *current.Value != *selected.Value)) {
			return false
		}
	}
	return true
}

func decimalFactsEqual(wanted, actual catalog.Value) bool {
	left, err := wanted.StringValue()
	if err != nil {
		return false
	}
	right, err := actual.StringValue()
	if err != nil {
		return false
	}
	a, ok := decimalFact(left)
	if !ok {
		return false
	}
	b, ok := decimalFact(right)
	return ok && a == b
}

func decimalFact(value string) (string, bool) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts[0]) == 0 || len(parts[0]) > 12 || (len(parts[0]) > 1 && parts[0][0] == '0') {
		return "", false
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return "", false
		}
	}
	if len(parts) == 1 {
		return parts[0], true
	}
	if len(parts[1]) > 4 {
		return "", false
	}
	fraction := strings.TrimRight(parts[1], "0")
	if fraction == "" {
		return parts[0], true
	}
	return parts[0] + "." + fraction, true
}
