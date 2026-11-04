package orders

import (
	"reflect"
	"slices"
)

func ExemplarStatusMatches(expected ExemplarSnapshot, status ExemplarStatus, validation bool) bool {
	if status.PostingNumber != expected.PostingNumber || len(status.Products) != len(expected.Products) {
		return false
	}
	if validation && status.Status != "ship_available" {
		return false
	}
	for _, product := range expected.Products {
		i := slices.IndexFunc(status.Products, func(p ExemplarStatusProduct) bool { return p.SKU == product.SKU })
		if i < 0 {
			return false
		}
		actual := status.Products[i]
		if len(actual.Exemplars) != len(product.Exemplars) {
			return false
		}
		seen := make(map[string]bool)
		for _, unit := range product.Exemplars {
			j := slices.IndexFunc(actual.Exemplars, func(u ExemplarInput) bool { return u.ID == unit.ID })
			if j < 0 || seen[unit.ID] || !exemplarUnitMatches(unit, actual.Exemplars[j]) {
				return false
			}
			seen[unit.ID] = true
			if validation && (j >= len(actual.Checks) || !exemplarChecksPassed(unit, actual.Checks[j])) {
				return false
			}
		}
	}
	return true
}

func exemplarUnitMatches(want, actual ExemplarInput) bool {
	for _, pair := range [][2]Field[string]{{want.GTD, actual.GTD}, {want.RNPT, actual.RNPT}} {
		if pair[0].IsKnown() && (!pair[1].IsKnown() || *pair[0].Value != *pair[1].Value) {
			return false
		}
		if pair[0].Presence == PresenceEmpty && pair[1].Presence != PresenceEmpty {
			return false
		}
		if !pair[0].IsKnown() && pair[1].IsKnown() {
			return false
		}
	}
	for _, pair := range [][2]Field[bool]{{want.GTDAbsent, actual.GTDAbsent}, {want.RNPTAbsent, actual.RNPTAbsent}} {
		if pair[0].IsKnown() && (!pair[1].IsKnown() || *pair[0].Value != *pair[1].Value) {
			return false
		}
	}
	if want.Marks.Presence == PresenceEmpty && actual.Marks.Presence != PresenceEmpty {
		return false
	}
	if want.Marks.IsKnown() != actual.Marks.IsKnown() {
		return false
	}
	if want.Marks.IsKnown() {
		a, b := slices.Clone(*want.Marks.Value), slices.Clone(*actual.Marks.Value)
		slices.SortFunc(a, compareExemplarMark)
		slices.SortFunc(b, compareExemplarMark)
		return reflect.DeepEqual(a, b)
	}
	return true
}

func compareExemplarMark(a, b ExemplarMark) int {
	if a.Type < b.Type || a.Type == b.Type && a.Value < b.Value {
		return -1
	}
	if a == b {
		return 0
	}
	return 1
}

func exemplarChecksPassed(u ExemplarInput, c ExemplarCheck) bool {
	if u.ID != c.ID || len(c.Errors) != 0 {
		return false
	}
	if u.GTD.IsKnown() && c.GTD != "passed" {
		return false
	}
	if u.RNPT.IsKnown() && c.RNPT != "passed" {
		return false
	}
	if u.Marks.IsKnown() {
		if len(c.Marks) != len(*u.Marks.Value) {
			return false
		}
		for _, status := range c.Marks {
			if status != "passed" {
				return false
			}
		}
	}
	return true
}

func ExemplarValidationFailed(s ExemplarStatus) bool {
	for _, p := range s.Products {
		for _, c := range p.Checks {
			if c.GTD == "failed" || c.RNPT == "failed" || slices.Contains(c.Marks, "failed") {
				return true
			}
		}
	}
	return false
}
