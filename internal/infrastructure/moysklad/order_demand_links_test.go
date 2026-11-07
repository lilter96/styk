package moysklad

import (
	"encoding/json"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func TestCustomerOrderDemandRelationshipPreservesAbsenceAndExactReferences(t *testing.T) {
	t.Parallel()
	id := "00000000-0000-4000-8000-000000000099"
	tests := []struct {
		name     string
		value    func(*OrderClient) any
		presence orders.Presence
		invalid  bool
	}{
		{name: "missing relationship remains unknown", presence: ""},
		{name: "null remains unknown", value: func(*OrderClient) any { return nil }, presence: orders.PresenceNull},
		{name: "explicit empty relationship", value: func(*OrderClient) any { return []any{} }, presence: orders.PresenceEmpty},
		{name: "exact draft demand identity", value: func(c *OrderClient) any { return []any{c.reference("entity/demand", "demand", id)} }, presence: orders.PresenceValue},
		{name: "duplicate identity", value: func(c *OrderClient) any { ref := c.reference("entity/demand", "demand", id); return []any{ref, ref} }, invalid: true},
		{name: "foreign provider host", value: func(*OrderClient) any {
			return []any{map[string]any{"meta": map[string]any{"type": "demand", "href": "https://untrusted.example/entity/demand/" + id}}}
		}, invalid: true},
		{name: "wrong document kind", value: func(c *OrderClient) any { return []any{c.reference("entity/supply", "supply", id)} }, invalid: true},
		{name: "invalid document id", value: func(c *OrderClient) any { return []any{c.reference("entity/demand", "demand", "not-a-uuid")} }, invalid: true},
		{name: "unexpected collection wrapper", value: func(*OrderClient) any { return map[string]any{"rows": []any{}} }, invalid: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, client := newOrderSimulator(t, "normal")
			fields := make(map[string]json.RawMessage)
			if tc.value != nil {
				raw, err := json.Marshal(tc.value(client))
				if err != nil {
					t.Fatal(err)
				}
				fields["demands"] = raw
			}
			links, err := client.orderLinkedDemandIDs(fields)
			if (err != nil) != tc.invalid {
				t.Fatal("demand relationship validation", err)
			}
			if tc.invalid {
				return
			}
			if links.Presence != tc.presence || (orders.AutomaticDemandCoverage(links).Code == "") != (tc.presence == orders.PresenceEmpty) {
				t.Fatal("unknown or existing demand became proven absence", links)
			}
		})
	}
}
