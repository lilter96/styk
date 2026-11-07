package ozon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestAccrualDayUsesSequentialSameDatePagesAndExactMoney(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		var request map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&request) != nil || r.Method != "POST" || r.URL.Path != accrualDayPath || len(request) != 2 || string(request["date"]) != `"2026-09-01"` || r.Header.Get("Client-Id") != "id" || r.Header.Get("Api-Key") == "" {
			t.Error("accrual request/authentication changed")
		}
		if call == 1 {
			if string(request["last_id"]) != `""` {
				t.Error("first cursor not empty")
			}
			writeTestResponse(t, w, `{"accruals":[{"accrual_id":9007199254740993,"accrued_category":"NON_ITEM","date":"2026-09-01","unit_number":"service","total_amount":{"amount":"-9007199254740993.00001","currency":"RUB"},"non_item_fee":{"accrued":{"amount":"-9007199254740993.00001","currency":"RUB"},"type_id":25},"posting":null}],"last_id":"opaque-page"}`)
			return
		}
		if call != 2 || string(request["last_id"]) != `"opaque-page"` {
			t.Error("cursor requests repeated or changed date")
		}
		writeTestResponse(t, w, `{"accruals":[{"accrual_id":9007199254740994,"accrued_category":"ITEM","date":"2026-09-01","total_amount":{"amount":"0","currency":"RUB"},"item_fees":{"fees":[{"sku":9007199254740993,"fees":[{"type_id":26,"accrued":{"amount":"0","currency":"RUB"}}]}]}}],"last_id":""}`)
	}))
	defer server.Close()
	c := newOrderTestClient(t, server, OrderReadOptions{})
	report, err := c.ReadAccrualReport(context.Background(), orderTestScope(), orders.AccrualRequest{Kind: orders.AccrualByDay, Date: "2026-09-01"})
	if err != nil || report.SourceFamily != "ACCRUAL" || report.Kind != orders.AccrualByDay || !report.Complete || report.Pages != 2 || report.RecordCount != 2 || calls.Load() != 2 {
		t.Fatal("bounded separate source failed", report, err)
	}
	assertAccrualNumber(t, report, "accruals[0].non_item_fee.accrued.amount", "-9007199254740993.00001", orders.PresenceValue)
	assertAccrualNumber(t, report, "accruals[1].item_fees.fees[0].sku", "9007199254740993", orders.PresenceValue)
	assertAccrualNumber(t, report, "accruals[1].total_amount.amount", "0", orders.PresenceZero)
	for _, fact := range report.Facts {
		if fact.Path == "last_id" {
			t.Fatal("temporary provider cursor escaped into source evidence")
		}
	}
}

func assertAccrualNumber(t *testing.T, report orders.AccrualReport, path, value string, presence orders.Presence) {
	t.Helper()
	for _, fact := range report.Facts {
		if fact.Path == path {
			if fact.Number == nil || string(*fact.Number) != value || fact.Presence != presence {
				t.Fatal("exact financial value changed", fact)
			}
			return
		}
	}
	t.Fatal("financial source path lost", path)
}

func TestAccrualUnknownFieldsRetainFingerprintWithoutArbitraryText(t *testing.T) {
	t.Parallel()
	read := func(amount string) orders.AccrualReport {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeTestResponse(t, w, `{"posting_accruals":[{"posting_number":"p1","accruals":[{"accrual_date":"2026-09-01","accrued":{"amount":"10.01","currency":"RUB"},"sku":null,"quantity":null,"seller_price":null,"type_id":1,"future_fee":`+amount+`,"private_extension":"PRIVATE_VALUE"}]}]}`)
		}))
		defer server.Close()
		c := newOrderTestClient(t, server, OrderReadOptions{})
		report, err := c.ReadAccrualReport(context.Background(), orderTestScope(), orders.AccrualRequest{Kind: orders.AccrualPostings, PostingNumbers: []string{"p1"}})
		if err != nil || report.Complete || len(report.UnknownFields) != 2 {
			t.Fatal("unknown field silently lost", report, err)
		}
		data, err := json.Marshal(report)
		if err != nil || strings.Contains(string(data), "PRIVATE_VALUE") {
			t.Fatal("arbitrary extension text escaped", err)
		}
		return report
	}
	first, second := read("1.00000000000000001"), read("1.00000000000000002")
	if first.Fingerprint == second.Fingerprint || len(first.Fingerprint) != 64 {
		t.Fatal("unknown financial correction not fingerprinted")
	}
}

func TestAccrualDayRejectsIncompleteReplayedOrMalformedEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, body string }{
		{"null rows", `{"accruals":null,"last_id":""}`},
		{"missing rows", `{"last_id":""}`},
		{"null cursor", `{"accruals":[],"last_id":null}`},
		{"numeric cursor", `{"accruals":[],"last_id":1}`},
		{"replayed cursor", `{"accruals":[],"last_id":"repeat"}`},
		{"duplicate JSON", `{"accruals":[],"accruals":[],"last_id":""}`},
		{"wrong day", `{"accruals":[{"accrual_id":1,"date":"2026-09-02"}],"last_id":""}`},
		{"duplicate accrual", `{"accruals":[{"accrual_id":1,"date":"2026-09-01"},{"accrual_id":1,"date":"2026-09-01"}],"last_id":""}`},
		{"binary numeric money", `{"accruals":[{"accrual_id":1,"date":"2026-09-01","total_amount":{"amount":1.01,"currency":"RUB"}}],"last_id":""}`},
		{"exponent amount", `{"accruals":[{"accrual_id":1,"date":"2026-09-01","total_amount":{"amount":"1e3","currency":"RUB"}}],"last_id":""}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeTestResponse(t, w, tc.body) }))
			defer server.Close()
			c := newOrderTestClient(t, server, OrderReadOptions{})
			_, err := c.ReadAccrualReport(context.Background(), orderTestScope(), orders.AccrualRequest{Kind: orders.AccrualByDay, Date: "2026-09-01"})
			if !errors.Is(err, orders.ErrSchemaDrift) {
				t.Fatal("incomplete source accepted", err)
			}
		})
	}
}

func TestAccrualProviderFailuresNeverBecomeEmptySourcesOrRetry(t *testing.T) {
	t.Parallel()
	for _, status := range []int{403, 404, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				writeTestResponse(t, w, `{"code":"FAILED"}`)
			}))
			defer server.Close()
			c := newOrderTestClient(t, server, OrderReadOptions{})
			report, err := c.ReadAccrualReport(context.Background(), orderTestScope(), orders.AccrualRequest{Kind: orders.AccrualTypes})
			var unavailable *orders.AccrualReadError
			if !errors.As(err, &unavailable) || report.Complete || calls.Load() != 1 {
				t.Fatal("failure became empty/was retried", report, err, calls.Load())
			}
		})
	}
}
