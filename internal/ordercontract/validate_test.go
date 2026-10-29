package ordercontract

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const validPosting = `{"result":{"posting_number":"TEST-1","order_id":1,"order_number":"TEST","status":"awaiting_packaging","products":[]}}`

func TestPostingPresenceTypesAndJSONIntegrity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, payload, code string
		valid               bool
	}{
		{name: "absent optional", payload: validPosting, valid: true},
		{name: "zero is present", payload: strings.Replace(validPosting, `"order_id":1`, `"order_id":0`, 1), valid: true},
		{name: "largest int64", payload: strings.Replace(validPosting, `"order_id":1`, `"order_id":9223372036854775807`, 1), valid: true},
		{name: "integer overflow", payload: strings.Replace(validPosting, `"order_id":1`, `"order_id":9223372036854775808`, 1), code: "wrong_type"},
		{name: "fractional identifier", payload: strings.Replace(validPosting, `"order_id":1`, `"order_id":1.2`, 1), code: "wrong_type"},
		{name: "missing required", payload: strings.Replace(validPosting, `"posting_number":"TEST-1",`, ``, 1), code: "missing_required"},
		{name: "required null", payload: strings.Replace(validPosting, `"posting_number":"TEST-1"`, `"posting_number":null`, 1), code: "null_not_allowed"},
		{name: "nullable cancellation", payload: strings.Replace(validPosting, `"products":[]`, `"products":[],"cancellation":null`, 1), valid: true},
		{name: "array element type", payload: strings.Replace(validPosting, `"products":[]`, `"products":["redacted"]`, 1), code: "wrong_type"},
		{name: "array element null", payload: strings.Replace(validPosting, `"products":[]`, `"products":[null]`, 1), code: "null_not_allowed"},
		{name: "nested duplicate", payload: strings.Replace(validPosting, `"order_id":1`, `"order_id":1,"order_id":2`, 1), code: "invalid_json"},
		{name: "trailing document", payload: validPosting + `{}`, code: "invalid_json"},
		{name: "invalid JSON", payload: `{"secret":"redacted"`, code: "invalid_json"},
	}
	manifest := testManifest(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report := manifest.Validate("/v3/posting/fbs/get", "response", []byte(tc.payload))
			if report.Valid != tc.valid || report.SchemaVerified || !hasCode(report, tc.code) {
				t.Fatalf("unexpected report: %+v", report)
			}
		})
	}
}

func TestNestedSchemaDriftDoesNotExposeKeyOrValue(t *testing.T) {
	t.Parallel()
	payload := strings.Replace(validPosting, `"products":[]`, `"products":[{"person@example.invalid":"secret personal value"}]`, 1)
	report := testManifest(t).Validate("/v3/posting/fbs/get", "response", []byte(payload))
	if report.Valid || len(report.DriftPaths) != 1 || !hasCode(report, "unknown_key") {
		t.Fatalf("drift not detected: %+v", report)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "person@") || strings.Contains(string(data), "secret personal value") {
		t.Fatal("diagnostic exposed source key or value")
	}
	if !strings.HasPrefix(report.DriftPaths[0], "$.result.products[].<unknown:") {
		t.Fatal("drift omitted known schema context")
	}
}

func TestUnverifiedCurrentLabelShapeCannotBecomeVerified(t *testing.T) {
	t.Parallel()
	manifest := testManifest(t)
	for _, endpoint := range []string{"/v3/posting/fbs/package-label/create", "/v2/posting/fbs/package-label/get"} {
		for _, direction := range []string{"request", "response"} {
			report := manifest.Validate(endpoint, direction, []byte(`{"unverified_field":1}`))
			if report.Valid || report.SchemaVerified || !hasCode(report, "unknown_key") {
				t.Fatalf("unverified label schema accepted: %+v", report)
			}
		}
	}
}

func TestPayloadAndDiagnosticBounds(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, payload string }{
		{name: "bytes", payload: strings.Repeat(" ", maxPayloadBytes+1)},
		{name: "depth", payload: strings.Repeat("[", maxDepth+2) + "0" + strings.Repeat("]", maxDepth+2)},
		{name: "nodes", payload: "[" + strings.Repeat("0,", maxNodes) + "0]"},
	}
	manifest := testManifest(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report := manifest.Validate("/v3/posting/fbs/get", "response", []byte(tc.payload))
			if report.Valid || !hasCode(report, "invalid_json") {
				t.Fatal("unbounded payload accepted")
			}
		})
	}
	var payload strings.Builder
	payload.WriteString(`{`)
	for i := range 200 {
		fmt.Fprintf(&payload, `"unknown%d":1,`, i)
	}
	payload.WriteString(`"final":1}`)
	report := manifest.Validate("/v3/posting/fbs/get", "response", []byte(payload.String()))
	if len(report.Issues) != maxIssues || len(report.DriftPaths) != maxIssues {
		t.Fatal("diagnostic bounds not enforced")
	}
}

func TestUnknownEndpointAndResponseOnlyMoyFieldsFailClosed(t *testing.T) {
	t.Parallel()
	manifest := testManifest(t)
	tests := []struct{ endpoint, direction, payload, code string }{
		{endpoint: "/v3/posting/fbs/list", direction: "request", payload: `{}`, code: "unknown_contract"},
		{endpoint: "/entity/customerorder/{id}", direction: "request", payload: `{"sum":123}`, code: "unknown_key"},
		{endpoint: "/entity/customerorder/{id}", direction: "request", payload: `{"syncId":"test"}`, code: "unknown_key"},
	}
	for _, tc := range tests {
		report := manifest.Validate(tc.endpoint, tc.direction, []byte(tc.payload))
		if report.Valid || !hasCode(report, tc.code) {
			t.Fatalf("unsafe contract accepted: %+v", report)
		}
	}
}

func hasCode(report Report, code string) bool {
	if code == "" {
		return true
	}
	for _, issue := range report.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
