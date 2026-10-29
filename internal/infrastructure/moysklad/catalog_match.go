package moysklad

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

func catalogBodyMatches(raw []byte, body map[string]any) bool {
	var observed map[string]json.RawMessage
	if json.Unmarshal(raw, &observed) != nil {
		return false
	}
	for key, want := range body {
		if key == "syncId" {
			continue // idempotency metadata need not be echoed by the provider.
		}
		encoded, err := json.Marshal(want)
		if err != nil || !catalogRawMatches(observed[key], encoded, key) {
			return false
		}
	}
	return true
}

func catalogRawMatches(observed, wanted []byte, key string) bool {
	var have, want any
	haveDecoder := json.NewDecoder(bytes.NewReader(observed))
	wantDecoder := json.NewDecoder(bytes.NewReader(wanted))
	haveDecoder.UseNumber()
	wantDecoder.UseNumber()
	if haveDecoder.Decode(&have) != nil || wantDecoder.Decode(&want) != nil {
		return false
	}
	return catalogJSONMatches(have, want, key)
}

func catalogJSONMatches(have, want any, key string) bool {
	switch desired := want.(type) {
	case map[string]any:
		actual, ok := have.(map[string]any)
		return ok && catalogObjectMatches(actual, desired)
	case []any:
		actual, ok := have.([]any)
		return ok && catalogArrayMatches(actual, desired, key)
	case json.Number:
		actual, ok := have.(json.Number)
		return ok && catalogNumberMatches(actual, desired)
	default:
		return have == want
	}
}

func catalogNumberMatches(have, want json.Number) bool {
	left, leftOK := catalogDecimal(have.String())
	right, rightOK := catalogDecimal(want.String())
	return leftOK && rightOK && left == right
}

func catalogDecimal(s string) (string, bool) {
	parts := strings.SplitN(strings.ToLower(s), "e", 2)
	exponent := int64(0)
	if len(parts) == 2 {
		var err error
		exponent, err = strconv.ParseInt(parts[1], 10, 32)
		if err != nil {
			return "", false
		}
	}
	sign, digits := "", parts[0]
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	if dot := strings.IndexByte(digits, '.'); dot >= 0 {
		exponent -= int64(len(digits) - dot - 1)
		digits = digits[:dot] + digits[dot+1:]
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return "0", true
	}
	trimmed := strings.TrimRight(digits, "0")
	exponent += int64(len(digits) - len(trimmed))
	return sign + trimmed + "e" + strconv.FormatInt(exponent, 10), true
}

func catalogObjectMatches(have, want map[string]any) bool {
	for key, value := range want {
		actual, exists := have[key]
		if !exists || !catalogJSONMatches(actual, value, key) {
			return false
		}
	}
	return true
}

func catalogArrayMatches(have, want []any, key string) bool {
	if len(have) != len(want) && key != "salePrices" && key != "attributes" {
		return false
	}
	used := make(map[int]bool, len(want))
	for _, desired := range want {
		found := false
		for i, actual := range have {
			if !used[i] && catalogJSONMatches(actual, desired, "") {
				used[i], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
