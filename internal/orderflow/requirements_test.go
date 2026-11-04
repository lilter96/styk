package orderflow

import "github.com/lilter96/styk/internal/orders"

func plainFlowRequirements() *orders.PackingRequirements {
	r := &orders.PackingRequirements{Presence: orders.PresenceValue, Products: make(map[orders.PackingRequirementKind]orders.Field[[]string])}
	for _, kind := range orders.PackingRequirementKinds() {
		r.Products[kind] = orders.Present([]string{})
	}
	return r
}
