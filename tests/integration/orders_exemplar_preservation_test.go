//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestOrdersExemplarHTTPPreservesNativeZeroWeightAndGrouping(t *testing.T) {
	runExemplarHTTP(t, "preserved_native_values")
}

// Provider v5 omits grouping. It is independently observed from the normal
// posting detail, while per-unit weight is independently re-read from status.
func exemplarPreservedHTTPBody(body, path string) (string, error) {
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewBufferString(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	if path == "/v3/posting/fbs/get" {
		value["result"].(map[string]any)["multi_box_qty"] = json.Number("1")
	}
	if path == "/v6/fbs/posting/product/exemplar/create-or-get" {
		value["multi_box_qty"] = json.Number("1")
	}
	if path == "/v6/fbs/posting/product/exemplar/create-or-get" || path == "/v5/fbs/posting/product/exemplar/status" {
		for _, product := range value["products"].([]any) {
			for _, unit := range product.(map[string]any)["exemplars"].([]any) {
				unit.(map[string]any)["weight"] = json.Number("0.000")
			}
		}
	}
	raw, err := json.Marshal(value)
	return string(raw), err
}
