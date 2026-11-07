package ordermoney

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestCommissionLegacyPreviewRetainsFrozenHashesWithoutNewComponent(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/commission_legacy_preview_20261107.json")
	if err != nil {
		t.Fatal(err)
	}
	var plan orders.CommissionPlan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil || string(encoded) != string(raw) || strings.Contains(string(encoded), "pick_up_point_coinvestment") {
		t.Fatal("old immutable preview serialization changed", err)
	}
	source, err := CommissionSourceFingerprint(plan.Source)
	if err != nil || source != "54366d0b22240e8653d2a350c40903ed149f8168a232ebb9c041ec725669c35c" || source != plan.Source.Fingerprint {
		t.Fatal("old frozen source fingerprint changed", err)
	}
	fingerprint, err := orders.CommissionPlanFingerprint(plan)
	if err != nil || fingerprint != "3f04dbe2d9d443d2ef5d45e64de3505737eb583cb3ff09dc26ec2c139778a96a" || fingerprint != plan.Fingerprint || plan.Validate() != nil {
		t.Fatal("old frozen plan hash or structural validity changed", err)
	}
	plan.Source.Rows[0].Delivery.PickUpPointCoinvestment = orders.Present(orders.Decimal("0"))
	changed, err := CommissionSourceFingerprint(plan.Source)
	if err != nil || changed == source {
		t.Fatal("newly observed component did not change source fingerprint", err)
	}
}

func TestCommissionDraftRequiresExplicitZeroPickUpPointComponent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value orders.Field[orders.Decimal]
	}{
		{"missing", orders.Field[orders.Decimal]{}},
		{"null", orders.Field[orders.Decimal]{Presence: orders.PresenceNull}},
		{"malformed known", orders.Field[orders.Decimal]{Presence: orders.PresenceValue}},
		{"positive", orders.Present(orders.Decimal("0.01"))},
		{"negative", orders.Present(orders.Decimal("-0.01"))},
	}
	for _, tc := range tests {
		for _, returned := range []bool{false, true} {
			name := tc.name + "/sale"
			if returned {
				name = tc.name + "/return"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				_, issues := commissionPlanFixture(t, func(_ *orders.Config, source *orders.RealizationReport, _ *[]orders.ProductMappingRef, _ *orders.CommissionReferences) {
					if returned {
						source.Rows[0].Return.PickUpPointCoinvestment = tc.value
						return
					}
					source.Rows[0].Delivery.PickUpPointCoinvestment = tc.value
				})
				if len(issues) != 1 || issues[0].Code != "finance_nonzero_or_unknown_components_require_review" {
					t.Fatal("unsupported component bypassed ZERO_ONLY policy", issues)
				}
			})
		}
	}
}
