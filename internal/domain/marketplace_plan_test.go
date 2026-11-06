package domain

import (
	"errors"
	"testing"
)

func TestMarketplacePlanAdmission(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		plan     MarketplacePlan
		usage    MarketplaceUsage
		provider string
		want     error
	}{
		{"first cabinet", "plan_1", MarketplaceUsage{"moysklad": 5}, "ozon", nil},
		{"one seat total", "plan_1", MarketplaceUsage{"ozon": 1}, "wildberries", ErrPlanTotalLimit},
		{"two distinct marketplaces", "plan_2", MarketplaceUsage{"ozon": 1}, "wildberries", nil},
		{"duplicate marketplace", "plan_2", MarketplaceUsage{"ozon": 1}, "ozon", ErrPlanProviderLimit},
		{"third marketplace", "plan_2", MarketplaceUsage{"ozon": 1, "wildberries": 1}, "yandex", ErrPlanTotalLimit},
		{"downgraded provider", "plan_2", MarketplaceUsage{"ozon": 2}, "yandex", ErrPlanOverLimit},
		{"fifth same marketplace", "plan_3", MarketplaceUsage{"ozon": 4}, "ozon", nil},
		{"sixth seat", "plan_3", MarketplaceUsage{"ozon": 5}, "ozon", ErrPlanTotalLimit},
		{"twentieth same marketplace", "plan_4", MarketplaceUsage{"ozon": 19}, "ozon", nil},
		{"twenty first seat", "plan_4", MarketplaceUsage{"ozon": 20}, "wildberries", ErrPlanTotalLimit},
		{"moysklad still available", "plan_1", MarketplaceUsage{"ozon": 2}, "moysklad", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			limits, ok := tt.plan.Limits()
			if !ok {
				t.Fatal("unknown fixture plan")
			}
			if err := limits.Admit(tt.usage, tt.provider); !errors.Is(err, tt.want) {
				t.Fatalf("admission = %v, want %v", err, tt.want)
			}
		})
	}
}
