package orders

import (
	"encoding/json"
	"slices"
	"strings"
)

func exemplarClone(s ExemplarSnapshot) (ExemplarSnapshot, error) {
	raw, e := json.Marshal(s)
	if e != nil || len(raw) > MaxExemplarBytes {
		return ExemplarSnapshot{}, ErrInvalid
	}
	var copy ExemplarSnapshot
	if json.Unmarshal(raw, &copy) != nil {
		return copy, ErrInvalid
	}
	return copy, nil
}

func canonicalExemplars(s *ExemplarSnapshot) {
	slices.SortFunc(s.Products, func(a, b ExemplarProduct) int { return strings.Compare(a.SKU, b.SKU) })
	for i := range s.Products {
		p := &s.Products[i]
		slices.SortFunc(p.Exemplars, func(a, b ExemplarInput) int { return strings.Compare(a.ID, b.ID) })
		for j := range p.Exemplars {
			u := &p.Exemplars[j]
			if u.Marks.IsKnown() {
				slices.SortFunc(*u.Marks.Value, func(a, b ExemplarMark) int { return strings.Compare(a.Type+":"+a.Value, b.Type+":"+b.Value) })
			}
		}
	}
}

func ExemplarSnapshotDigest(s ExemplarSnapshot) ([]byte, error) {
	copy, e := exemplarClone(s)
	if e != nil {
		return nil, e
	}
	canonicalExemplars(&copy)
	return json.Marshal(copy)
}
