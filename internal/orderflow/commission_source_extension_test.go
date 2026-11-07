package orderflow

import (
	"context"
	"testing"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func legacyCommissionSource(t *testing.T, store *commissionFlowStore, provider *commissionFlowProvider) {
	t.Helper()
	work := store.financeWork
	work.Plan.Source.Rows[0].Delivery.PickUpPointCoinvestment = orders.Field[orders.Decimal]{}
	work.Plan.Source.Rows[0].Return.PickUpPointCoinvestment = orders.Field[orders.Decimal]{}
	var err error
	work.Plan.Source.Fingerprint, err = ordermoney.CommissionSourceFingerprint(work.Plan.Source)
	if err != nil {
		t.Fatal(err)
	}
	work.Plan.Fingerprint, err = orders.CommissionPlanFingerprint(work.Plan)
	if err != nil || work.Plan.Validate() != nil {
		t.Fatal("legacy immutable intent structurally invalid", err)
	}
	provider.report = work.Plan.Source
}

func TestCommissionRunnerLegacyAbsentComponentBlocksOnlyUnsentWrite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		sent     bool
		expected orders.State
	}{
		{"legacy unsent", false, orders.StateBlocked},
		{"own legacy unknown", true, orders.StateReconciled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			service, runner, store, provider := commissionFlowFixture(t)
			commissionFlowApprove(t, service, store, provider)
			if tc.sent {
				provider.lost = true
				if _, err := runner.RunNext(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			legacyCommissionSource(t, store, provider)
			writes := provider.writes
			if _, err := runner.RunNext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if provider.writes != writes || store.financeWork.State != tc.expected {
				t.Fatal("legacy write gate or own UNKNOWN readback changed", store.financeWork.State, provider.writes)
			}
			if !tc.sent && store.financeWork.Code != "FINANCE_SOURCE_COMPONENTS_REQUIRE_REVIEW" {
				t.Fatal("missing component was not diagnosed before dispatch", store.financeWork.Code)
			}
		})
	}
}
