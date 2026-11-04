package orders

import "testing"

func TestSupersededSplitMarkerCannotAuthorizeOtherNativeMutations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*Command)
	}{
		{"scalar action", func(c *Command) { c.Action = ActionMirrorUpdate }},
		{"missing patch", func(c *Command) { c.PositionPatch = nil }},
		{"positive reserve", func(c *Command) { c.PositionPatch.Reserve = Present(Decimal("1")) }},
		{"unknown reserve", func(c *Command) { c.PositionPatch.Reserve = Field[Decimal]{} }},
		{"quantity change", func(c *Command) { c.PositionPatch.Quantity = Present(Decimal("1")) }},
		{"price change", func(c *Command) { c.PositionPatch.Price = Present(Money{Amount: "1", Currency: "RUB"}) }},
		{"unposting", func(c *Command) { c.CustomerOrder.Applicable = Present(false) }},
		{"scalar field", func(c *Command) { c.CustomerOrder.OwnedFields = append(c.CustomerOrder.OwnedFields, "name") }},
		{"retained identity", func(c *Command) { c.RetainedSplitWorkID = "another-split" }},
		{"unencumbered identity", func(c *Command) { c.SplitSourceWorkID = "another-split" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			facts, _ := supersededTransferFixture(t)
			command := facts.ParentRelease.Command
			if err := command.Validate(); err != nil {
				t.Fatal("valid exact reserve release rejected", err)
			}
			tc.change(&command)
			if command.Validate() == nil {
				t.Fatal("supersession marker authorized an unrelated mutation")
			}
		})
	}
}
