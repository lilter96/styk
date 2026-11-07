package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func realizationFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/realization_20261106/report.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRealizationReaderUsesExactMonthlyContractAndNoBinaryMoney(t *testing.T) {
	t.Parallel()
	fixture := realizationFixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request orders.RealizationPeriod
		if r.Method != "POST" || r.URL.Path != financeRealizationPath || r.Header.Get("Client-Id") != "id" || json.NewDecoder(r.Body).Decode(&request) != nil || request != (orders.RealizationPeriod{Year: 2026, Month: 9}) {
			t.Error("monthly report request/authentication contract changed")
		}
		writeTestResponse(t, w, string(fixture))
	}))
	defer server.Close()
	client := newOrderTestClient(t, server, OrderReadOptions{})
	client.now = func() time.Time { return time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC) }
	report, err := client.ReadRealizationReport(context.Background(), orderTestScope(), orders.RealizationPeriod{Year: 2026, Month: 9})
	if err != nil {
		t.Fatal(err)
	}
	row := report.Rows[0]
	if calls.Load() != 1 || report.Currency != "RUB" || len(report.Fingerprint) != 64 || len(report.Blockers) != 3 || *row.SKU.Value != "9007199254740993" || *row.SellerUnitPrice.Value != "1234567890123456.78" || *row.Delivery.Total.Value != "2469135780246913.575" || *row.Delivery.StandardFee.Value != "-0.01" {
		t.Fatal("authoritative report amounts or identity lost precision")
	}
	if row.Delivery.Stars.Presence != orders.PresenceNull || !row.Delivery.LegacyCommission.IsMissing() || row.Return.Quantity.Presence != orders.PresenceZero {
		t.Fatal("null, absent and zero financial evidence flattened")
	}
	encoded, err := json.Marshal(report)
	if err != nil || strings.Contains(string(encoded), "PRIVATE") {
		t.Fatal("legal identity escaped into report preview")
	}
}

func TestRealizationRejectsIncompleteAndDriftedReports(t *testing.T) {
	t.Parallel()
	valid := string(realizationFixture(t))
	tests := []struct{ name, body string }{
		{"legacy envelope", `{"header":{},"rows":[]}`},
		{"missing report", `{"result":null}`},
		{"null rows", strings.Replace(valid, `"rows": [`, `"rows": null, "discard": [`, 1)},
		{"duplicate JSON keys", strings.Replace(valid, `"rowNumber": 1`, `"rowNumber": 1,"rowNumber": 2`, 1)},
		{"quoted amount", strings.Replace(valid, `"standard_fee": -0.01`, `"standard_fee": "-0.01"`, 1)},
		{"approximate exponent", strings.Replace(valid, `"standard_fee": -0.01`, `"standard_fee": -1e-2`, 1)},
		{"wrong quantity", strings.Replace(valid, `"quantity": 2`, `"quantity": 2.5`, 1)},
		{"unknown currency", strings.Replace(valid, `"RUB"`, `"rub"`, 1)},
		{"missing SKU", strings.Replace(valid, `"sku": 9007199254740993,`, ``, 1)},
		{"truncated", valid[:len(valid)/2]},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeRealization([]byte(tc.body), orderTestScope(), orders.RealizationPeriod{Year: 2026, Month: 9}, time.Now())
			if !errors.Is(err, orders.ErrSchemaDrift) {
				t.Fatalf("report accepted without exact evidence: %v", err)
			}
		})
	}
}

func TestRealizationFingerprintIgnoresPrivateHeaderAndDetectsCorrections(t *testing.T) {
	t.Parallel()
	valid := string(realizationFixture(t))
	decode := func(body string) orders.RealizationReport {
		t.Helper()
		report, err := decodeRealization([]byte(body), orderTestScope(), orders.RealizationPeriod{Year: 2026, Month: 9}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	original := decode(valid)
	privateChange := decode(strings.Replace(valid, "PRIVATE_PAYER", "CHANGED_PRIVATE_PAYER", 1))
	correction := decode(strings.Replace(valid, `"standard_fee": -0.01`, `"standard_fee": -0.02`, 1))
	if original.Fingerprint != privateChange.Fingerprint || original.Fingerprint == correction.Fingerprint {
		t.Fatal("financial fingerprint missed correction or included legal PII")
	}
}

func TestRealizationProviderFailureIsNotRetriedOrReportedAsEmpty(t *testing.T) {
	t.Parallel()
	for _, status := range []int{403, 404, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(status)
				writeTestResponse(t, w, `{"message":"PRIVATE_PROVIDER_ERROR"}`)
			}))
			defer server.Close()
			client := newOrderTestClient(t, server, OrderReadOptions{})
			_, err := client.ReadRealizationReport(context.Background(), orderTestScope(), orders.RealizationPeriod{Year: 2026, Month: 9})
			if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("provider failure became success, retried, or exposed data")
			}
		})
	}
}
