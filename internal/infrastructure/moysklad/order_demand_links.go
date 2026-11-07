package moysklad

import (
	"encoding/json"

	"github.com/lilter96/styk/internal/orders"
)

// The documented customerorder.demands relationship includes drafts. Missing
// or null is unknown coverage; only an explicit empty array proves no links.
func (c *OrderClient) orderLinkedDemandIDs(raw map[string]json.RawMessage) (orders.Field[[]string], error) {
	value, present := raw["demands"]
	if !present {
		return orders.Field[[]string]{}, nil
	}
	if string(value) == "null" {
		return orders.Field[[]string]{Presence: orders.PresenceNull}, nil
	}
	var references []catalogReference
	if json.Unmarshal(value, &references) != nil || references == nil || len(references) > 1000 {
		return orders.Field[[]string]{}, ErrOrderSchema
	}
	ids := make([]string, 0, len(references))
	seen := make(map[string]bool, len(references))
	for _, reference := range references {
		id, err := c.referenceID(reference, "entity/demand", "demand")
		if err != nil || !orderUUID.MatchString(id) || seen[id] {
			return orders.Field[[]string]{}, ErrOrderIdentity
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return orders.Present(ids), nil
}
