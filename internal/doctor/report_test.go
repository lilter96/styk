package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func status(r Report, code string) Status {
	for _, c := range r.Checks {
		if c.Code == code {
			return c.Status
		}
	}
	return ""
}

func TestEvaluateKeepsIncompleteEvidenceUnknown(t *testing.T) {
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		e    Evidence
		code string
		want Status
	}{
		{"unregistered accounts", Evidence{}, "ACCOUNT_REGISTRATION", Fail},
		{"missing import", Evidence{MoyskladRegistered: true, OzonRegistered: true}, "MOYSKLAD_IMPORT", Warn},
		{"incomplete import", Evidence{MoyskladRegistered: true, Moysklad: Batch{Present: true}}, "MOYSKLAD_IMPORT", Fail},
		{"old import", Evidence{MoyskladRegistered: true, Moysklad: Batch{Present: true, Completed: true, ObservedAt: now.Add(-2 * time.Hour)}}, "MOYSKLAD_IMPORT", Warn},
		{"truncated", Evidence{CorrelationsChecked: true, CorrelationsTruncated: true}, "ORDER_CORRELATIONS", Warn},
		{"ambiguous", Evidence{CorrelationsChecked: true, AmbiguousCorrelations: 2}, "ORDER_CORRELATIONS", Warn},
		{"missing mappings", Evidence{MoyskladRegistered: true, OzonRegistered: true}, "PRODUCT_MAPPINGS", Warn},
		{"unregistered stock", Evidence{}, "OZON_STOCK_COVERAGE", NotChecked},
		{"reported stock is not complete coverage", Evidence{OzonRegistered: true, Ozon: Batch{Present: true, Completed: true, ObservedAt: now, Counts: map[string]int64{"stock_rows": 5}}}, "OZON_STOCK_COVERAGE", Warn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Evaluate(tc.e, now, time.Hour)
			if got := status(r, tc.code); got != tc.want {
				t.Fatalf("%s: %s", tc.code, got)
			}
			if r.Publication != "DISABLED" || status(r, "RESERVATION_ATTRIBUTION") != Warn {
				t.Fatal("safety uncertainty lost")
			}
		})
	}
}

func TestReportJSONDeterministicAndNoRawIdentifiers(t *testing.T) {
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	from, to := now.Add(-time.Hour), now
	e := Evidence{MoyskladRegistered: true, OzonRegistered: true, Postings: Batch{Present: true, Completed: true, ObservedAt: now, WindowFrom: &from, WindowTo: &to, Counts: map[string]int64{"postings": 2}}, DecisionStatus: "UNKNOWN", DecisionReasons: []string{"Z_REASON", "A_REASON"}}
	a, _ := json.Marshal(Evaluate(e, now, time.Hour))
	b, _ := json.Marshal(Evaluate(e, now, time.Hour))
	if string(a) != string(b) || !strings.Contains(string(a), `"window_from"`) || !strings.Contains(string(a), `"A_REASON","Z_REASON"`) {
		t.Fatalf("unexpected report: %s", a)
	}
	for _, secret := range []string{"Authorization", "Bearer", "customer_name", "address"} {
		if strings.Contains(string(a), secret) {
			t.Fatalf("unexpected field %q", secret)
		}
	}
	if got := Evaluate(Evidence{DecisionStatus: "READY_CONDITIONAL"}, now, time.Hour).DecisionStatus; got != "NOT_CHECKED" {
		t.Fatalf("unexpected ready status: %s", got)
	}
	if got := status(Evaluate(Evidence{}, now, time.Hour), "CALCULATION_SCOPE"); got != NotChecked {
		t.Fatalf("missing pool scope: %s", got)
	}
}

func TestLiveMissingCredentialsAndRedaction(t *testing.T) {
	r := Evaluate(Evidence{}, time.Now(), time.Hour)
	Live(context.Background(), &r, nil, func(context.Context) (bool, error) { return false, errors.New("secret customer raw response") })
	if status(r, "MOYSKLAD_CONNECTION") != NotChecked || status(r, "OZON_CONNECTION") != Fail {
		t.Fatal("probe outcomes lost")
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "secret customer") {
		t.Fatal("probe error leaked")
	}
}

func TestCatalogProgressIsOptionalAndSeparateFromHistoricalMappings(t *testing.T) {
	now := time.Now()
	if _, present := Evaluate(Evidence{ProductMappings: 99}, now, time.Hour).MappingCounts["catalog_products"]; present {
		t.Fatal("unknown catalog total was fabricated")
	}
	total, confirmed := int64(9), int64(4)
	r := Evaluate(Evidence{ProductMappings: 99, UnconfirmedProducts: 5, CatalogProducts: &total, CatalogConfirmedProducts: &confirmed}, now, time.Hour)
	if r.MappingCounts["catalog_products"] != 9 || r.MappingCounts["catalog_confirmed_products"] != 4 || r.MappingCounts["confirmed_products"] != 99 || r.MappingCounts["unconfirmed_products"] != 5 {
		t.Fatal("catalog and historical mapping semantics were mixed")
	}
	total, confirmed = 0, 0
	if got, present := Evaluate(Evidence{CatalogProducts: &total, CatalogConfirmedProducts: &confirmed}, now, time.Hour).MappingCounts["catalog_products"]; !present || got != 0 {
		t.Fatal("known empty catalog was treated as unknown")
	}
}
