package domain

import "errors"

var (
	ErrPlanTotalLimit    = errors.New("marketplace plan total limit")
	ErrPlanProviderLimit = errors.New("marketplace plan provider limit")
	ErrPlanOverLimit     = errors.New("marketplace plan exceeded")
)

type MarketplacePlan string

type MarketplaceLimits struct {
	Code        MarketplacePlan `json:"code"`
	Total       int             `json:"total_limit"`
	PerProvider int             `json:"provider_limit"`
}

var marketplacePlans = [...]MarketplaceLimits{
	{"plan_1", 1, 1},
	{"plan_2", 2, 1},
	{"plan_3", 5, 5},
	{"plan_4", 20, 20},
}

func MarketplacePlans() []MarketplaceLimits {
	return append([]MarketplaceLimits(nil), marketplacePlans[:]...)
}

func (p MarketplacePlan) Limits() (MarketplaceLimits, bool) {
	for _, limits := range marketplacePlans {
		if limits.Code == p {
			return limits, true
		}
	}
	return MarketplaceLimits{}, false
}

// Marketplace providers are canonical keys validated at the connection boundary.
func CountsTowardMarketplacePlan(provider string) bool {
	return provider != "" && provider != "moysklad"
}

type MarketplaceUsage map[string]int

func (u MarketplaceUsage) Total() int {
	total := 0
	for provider, count := range u {
		if CountsTowardMarketplacePlan(provider) {
			total += count
		}
	}
	return total
}

func (l MarketplaceLimits) Exceeded(u MarketplaceUsage) bool {
	if u.Total() > l.Total {
		return true
	}
	for provider, count := range u {
		if CountsTowardMarketplacePlan(provider) && count > l.PerProvider {
			return true
		}
	}
	return false
}

func (l MarketplaceLimits) Admit(u MarketplaceUsage, provider string) error {
	switch {
	case !CountsTowardMarketplacePlan(provider):
		return nil
	case l.Exceeded(u):
		return ErrPlanOverLimit
	case u.Total() >= l.Total:
		return ErrPlanTotalLimit
	case u[provider] >= l.PerProvider:
		return ErrPlanProviderLimit
	default:
		return nil
	}
}
