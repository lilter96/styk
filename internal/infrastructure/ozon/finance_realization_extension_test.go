package ozon

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

const pickUpPointFixtureAmount = "9007199254740993.00000000000000001"

func realizationExtensionFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/realization_live_extension_20261107/report.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func decodeRealizationExtension(t *testing.T, body string) orders.RealizationReport {
	t.Helper()
	report, err := decodeRealization([]byte(body), orderTestScope(), orders.RealizationPeriod{Year: 2026, Month: 10}, time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestRealizationLiveExtensionPreservesExactMoneyAndNullReturn(t *testing.T) {
	t.Parallel()
	report := decodeRealizationExtension(t, realizationExtensionFixture(t))
	row := report.Rows[0]
	if !row.Delivery.PickUpPointCoinvestment.IsKnown() || string(*row.Delivery.PickUpPointCoinvestment.Value) != pickUpPointFixtureAmount {
		t.Fatal("live-observed financial component lost precision")
	}
	if row.Return.Presence != orders.PresenceNull || row.Return.Quantity.IsKnown() || row.Return.PickUpPointCoinvestment.IsKnown() {
		t.Fatal("null return evidence became known zero")
	}
	changed := decodeRealizationExtension(t, strings.Replace(realizationExtensionFixture(t), pickUpPointFixtureAmount, "9007199254740993.00000000000000002", 1))
	if report.Fingerprint == changed.Fingerprint {
		t.Fatal("changed live component did not change source fingerprint")
	}
}

func TestRealizationLiveExtensionPreservesMissingNullAndZero(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, replacement string
		presence          orders.Presence
		retained          bool
	}{
		{"missing", "", orders.PresenceMissing, false},
		{"null", `"pick_up_point_coinvestment": null,`, orders.PresenceNull, true},
		{"zero", `"pick_up_point_coinvestment": 0,`, orders.PresenceZero, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := strings.Replace(realizationExtensionFixture(t), `"pick_up_point_coinvestment": `+pickUpPointFixtureAmount+`,`, tc.replacement, 1)
			report := decodeRealizationExtension(t, body)
			amount := report.Rows[0].Delivery.PickUpPointCoinvestment
			presence := amount.Presence
			if amount.IsMissing() {
				presence = orders.PresenceMissing
			}
			if presence != tc.presence {
				t.Fatal("financial component presence changed", amount.Presence)
			}
			raw, err := json.Marshal(report.Rows[0].Delivery)
			if err != nil || strings.Contains(string(raw), "pick_up_point_coinvestment") != tc.retained {
				t.Fatal("missing compatibility or retained source evidence changed", err)
			}
		})
	}
}

func TestRealizationLiveExtensionStillRejectsUnknownAndInvalidAmounts(t *testing.T) {
	t.Parallel()
	tests := []string{`"1.00"`, `1e-2`, `true`, `{}`, `[]`}
	for _, amount := range tests {
		t.Run(amount, func(t *testing.T) {
			t.Parallel()
			body := strings.Replace(realizationExtensionFixture(t), pickUpPointFixtureAmount, amount, 1)
			_, err := decodeRealization([]byte(body), orderTestScope(), orders.RealizationPeriod{Year: 2026, Month: 10}, time.Now())
			if !errors.Is(err, orders.ErrSchemaDrift) {
				t.Fatal("unreviewed amount format accepted", err)
			}
		})
	}
	body := strings.Replace(realizationExtensionFixture(t), `"pick_up_point_coinvestment"`, `"another_coinvestment"`, 1)
	report, err := decodeRealization([]byte(body), orderTestScope(), orders.RealizationPeriod{Year: 2026, Month: 10}, time.Now())
	if err != nil || len(report.UnknownComponents) != 1 || report.UnknownComponents[0].Path != "delivery_commission.another_coinvestment" || len(report.UnknownComponents[0].ValueDigest) != 64 {
		t.Fatal("unknown financial component was lost instead of retained for review", err)
	}
}
