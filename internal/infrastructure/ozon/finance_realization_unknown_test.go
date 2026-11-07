package ozon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func TestRealizationUnknownComponentsRetainExactEvidenceWithoutArbitraryText(t *testing.T) {
	t.Parallel()
	base := string(realizationFixture(t))
	tests := []struct {
		name, body string
		count      int
	}{
		{"numericrowcomponent", strings.Replace(base, `"bonus": 0`, `"bonus": 0,"future_charge": -9007199254740993.01`, 1), 1},
		{"opaqueheader", strings.Replace(base, `"number":`, `"future_contact": "private-future-value","number":`, 1), 1},
		{"futureenvelope", strings.Replace(base, `"result":`, `"future_sum": 2,"result":`, 1), 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report, err := decodeRealization([]byte(tc.body), orderTestScope(), orders.RealizationPeriod{Year: 2026, Month: 9}, time.Now())
			if err != nil || len(report.UnknownComponents) != tc.count {
				t.Fatal("unknown component discarded", err)
			}
			ledger, err := ordermoney.ClassifyRealization(report)
			if err != nil || ledger.UnknownComponents != tc.count || ledger.Complete || ordermoney.CommissionSourceComponentsSupported(report) {
				t.Fatal("unknown field enabled complete import", err)
			}
			raw, err := json.Marshal(report)
			if err != nil || strings.Contains(string(raw), "private-future-value") {
				t.Fatal("unknown text retained as PII", err)
			}
			changed := strings.Replace(tc.body, "-9007199254740993.01", "-9007199254740993.02", 1)
			if changed != tc.body {
				next, err := decodeRealization([]byte(changed), orderTestScope(), report.Period, time.Now())
				if err != nil || next.Fingerprint == report.Fingerprint {
					t.Fatal("unknown numeric correction missed", err)
				}
			}
		})
	}
}
