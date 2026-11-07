package orders

import (
	"sort"
	"strconv"
)

type FinancialComponentChange struct {
	SourceRow    int64          `json:"source_row"`
	Side         string         `json:"side"`
	Component    string         `json:"component"`
	Kind         string         `json:"kind"`
	Before       Field[Decimal] `json:"before"`
	After        Field[Decimal] `json:"after"`
	BeforeDigest string         `json:"before_digest,omitempty"`
	AfterDigest  string         `json:"after_digest,omitempty"`
}

func DiffFinancialLedgers(before, after FinancialLedger) ([]FinancialComponentChange, error) {
	if before.Period != after.Period || before.Validate() != nil || after.Validate() != nil {
		return nil, ErrInvalid
	}
	prior := make(map[string]FinancialComponent, len(before.Components))
	for _, component := range before.Components {
		prior[financialChangeKey(component)] = component
	}
	changes := make([]FinancialComponentChange, 0)
	for _, component := range after.Components {
		key := financialChangeKey(component)
		previous, found := prior[key]
		delete(prior, key)
		if found && financialComponentValueEqual(previous, component) {
			continue
		}
		kind := "CHANGED"
		if !found {
			kind = "ADDED"
		}
		changes = append(changes, FinancialComponentChange{SourceRow: component.SourceRow, Side: component.Side, Component: component.Component, Kind: kind, Before: previous.Value, After: component.Value, BeforeDigest: previous.ValueDigest, AfterDigest: component.ValueDigest})
	}
	for _, component := range prior {
		changes = append(changes, FinancialComponentChange{SourceRow: component.SourceRow, Side: component.Side, Component: component.Component, Kind: "REMOVED", Before: component.Value, BeforeDigest: component.ValueDigest})
	}
	sort.Slice(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.SourceRow != b.SourceRow {
			return a.SourceRow < b.SourceRow
		}
		if a.Side != b.Side {
			return a.Side < b.Side
		}
		return a.Component < b.Component
	})
	return changes, nil
}

func financialChangeKey(component FinancialComponent) string {
	return strconv.FormatInt(component.SourceRow, 10) + ":" + component.Side + ":" + component.Component
}

func financialComponentValueEqual(left, right FinancialComponent) bool {
	if left.ValueDigest != right.ValueDigest || left.Currency != right.Currency || left.SourcePresence != right.SourcePresence {
		return false
	}
	if left.Value.IsMissing() && right.Value.IsMissing() {
		return true
	}
	if left.Value.Presence != right.Value.Presence {
		return false
	}
	if !left.Value.IsKnown() {
		return true
	}
	return left.Value.Value != nil && right.Value.Value != nil && ExactDecimalEqual(*left.Value.Value, *right.Value.Value)
}
