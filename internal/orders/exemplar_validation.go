package orders

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
)

func exemplarID(s string) bool {
	n, e := strconv.ParseInt(s, 10, 64)
	return e == nil && n > 0 && strconv.FormatInt(n, 10) == s
}

func (s ExemplarSnapshot) ValidateFor(p Posting) error {
	if s.PostingNumber != p.PostingNumber || len(s.Products) == 0 || len(s.Products) > 1000 {
		return ErrStale
	}
	want := make(map[string]int64, len(p.Lines))
	for _, line := range p.Lines {
		n, e := strconv.ParseInt(string(line.Quantity), 10, 64)
		if e != nil || n <= 0 || n > 2147483647 || !exemplarID(line.Identity.SKU) || want[line.Identity.SKU] > 2147483647-n {
			return ErrInvalid
		}
		want[line.Identity.SKU] += n
	}
	seen := make(map[string]bool)
	marks := make(map[string]bool)
	for _, product := range s.Products {
		if product.Quantity != want[product.SKU] || product.Quantity <= 0 || product.Quantity > 1000 || len(product.Exemplars) != int(product.Quantity) {
			return ErrStale
		}
		delete(want, product.SKU)
		for _, unit := range product.Exemplars {
			if !exemplarID(unit.ID) || seen[unit.ID] || validateExemplar(unit) != nil {
				return ErrInvalid
			}
			seen[unit.ID] = true
			if unit.Marks.IsKnown() {
				for _, mark := range *unit.Marks.Value {
					key := mark.Type + ":" + mark.Value
					if marks[key] {
						return ErrConflict
					}
					marks[key] = true
				}
			}
		}
	}
	if len(want) != 0 {
		return ErrStale
	}
	return nil
}

func validateExemplar(u ExemplarInput) error {
	clear := make(map[string]bool, len(u.ClearMarkTypes))
	for _, kind := range u.ClearMarkTypes {
		if clear[kind] || !slices.Contains([]string{"mandatory_mark", "jw_uin", "imei"}, kind) {
			return ErrInvalid
		}
		clear[kind] = true
	}
	if u.GTD.IsKnown() && u.GTDAbsent.IsKnown() && *u.GTDAbsent.Value {
		return ErrConflict
	}
	if u.RNPT.IsKnown() && u.RNPTAbsent.IsKnown() && *u.RNPTAbsent.Value {
		return ErrConflict
	}
	for _, f := range []Field[string]{u.GTD, u.RNPT} {
		if f.Validate() != nil || (f.IsKnown() && !exemplarText(*f.Value)) {
			return ErrInvalid
		}
	}
	if u.GTDAbsent.Validate() != nil || u.RNPTAbsent.Validate() != nil || u.Marks.Validate() != nil {
		return ErrInvalid
	}
	if !u.Marks.IsKnown() {
		return nil
	}
	if len(*u.Marks.Value) > 20 {
		return ErrInvalid
	}
	seen := make(map[string]bool)
	for _, mark := range *u.Marks.Value {
		if clear[mark.Type] {
			return ErrConflict
		}
		if !slices.Contains([]string{"mandatory_mark", "jw_uin", "imei"}, mark.Type) || !exemplarText(mark.Value) || seen[mark.Type+":"+mark.Value] {
			return ErrInvalid
		}
		seen[mark.Type+":"+mark.Value] = true
	}
	return nil
}

func exemplarText(s string) bool {
	return len(s) > 0 && len(s) <= 4096 && !strings.ContainsAny(s, "\x00\r\n")
}

// ApplyExemplarInput preserves provider values for fields the merchant did not
// select. Every identifier must already belong to the exact prepared snapshot.
func ApplyExemplarInput(base ExemplarSnapshot, input ExemplarSetInput, p Posting) (ExemplarSnapshot, error) {
	if base.ValidateFor(p) != nil {
		return base, ErrStale
	}
	raw, err := exemplarClone(base)
	if err != nil {
		return base, err
	}
	bySKU := make(map[string]int, len(raw.Products))
	for i, v := range raw.Products {
		bySKU[v.SKU] = i
	}
	seen := make(map[string]bool)
	for _, product := range input.Products {
		i, ok := bySKU[product.SKU]
		if !ok {
			return base, ErrStale
		}
		for _, patch := range product.Exemplars {
			key := product.SKU + ":" + patch.ID
			if seen[key] || validateExemplar(patch) != nil {
				return base, ErrInvalid
			}
			seen[key] = true
			j := slices.IndexFunc(raw.Products[i].Exemplars, func(u ExemplarInput) bool { return u.ID == patch.ID })
			if j < 0 {
				return base, ErrStale
			}
			if (patch.Marks.IsKnown() || len(patch.ClearMarkTypes) > 0) && !raw.Products[i].Exemplars[j].Marks.IsKnown() && raw.Products[i].Exemplars[j].Marks.Presence != PresenceEmpty {
				return base, ErrBlocked
			}
			raw.Products[i].Exemplars[j] = mergeExemplar(raw.Products[i].Exemplars[j], patch)
		}
	}
	if len(seen) == 0 {
		return base, ErrInvalid
	}
	return raw, nil
}

func mergeExemplar(base, patch ExemplarInput) ExemplarInput {
	base.GTD, base.GTDAbsent = mergeExemplarDeclaration(base.GTD, base.GTDAbsent, patch.GTD, patch.GTDAbsent)
	base.RNPT, base.RNPTAbsent = mergeExemplarDeclaration(base.RNPT, base.RNPTAbsent, patch.RNPT, patch.RNPTAbsent)
	base.Marks = mergeExemplarMarks(base.Marks, patch.Marks, patch.ClearMarkTypes)
	base.ClearMarkTypes = nil
	return base
}

func mergeExemplarMarks(base, patch Field[[]ExemplarMark], clear []string) Field[[]ExemplarMark] {
	replace := make(map[string]bool, len(clear))
	for _, kind := range clear {
		replace[kind] = true
	}
	if patch.IsKnown() {
		for _, mark := range *patch.Value {
			replace[mark.Type] = true
		}
	}
	if len(replace) == 0 {
		return base
	}
	values := make([]ExemplarMark, 0)
	if base.IsKnown() {
		for _, mark := range *base.Value {
			if !replace[mark.Type] {
				values = append(values, mark)
			}
		}
	}
	if patch.IsKnown() {
		values = append(values, (*patch.Value)...)
	}
	return Present(values)
}

func ExemplarValuesEqual(a, b ExemplarSnapshot) bool {
	left, e := exemplarClone(a)
	if e != nil {
		return false
	}
	right, e := exemplarClone(b)
	if e != nil {
		return false
	}
	canonicalExemplars(&left)
	canonicalExemplars(&right)
	for i := range left.Products {
		left.Products[i].Required = nil
	}
	for i := range right.Products {
		right.Products[i].Required = nil
	}
	return reflect.DeepEqual(left, right)
}

// An explicit absence declaration clears the previous value. A new factual
// value implies presence unless the merchant supplied a contradictory flag;
// contradictory explicit input is rejected before merging.
func mergeExemplarDeclaration(value Field[string], absent Field[bool], patch Field[string], flag Field[bool]) (Field[string], Field[bool]) {
	if patch.IsSpecified() {
		value = patch
	}
	if flag.IsSpecified() {
		absent = flag
	}
	if flag.IsKnown() && *flag.Value && !patch.IsSpecified() {
		value = Present("")
	}
	if patch.IsKnown() && !flag.IsSpecified() {
		absent = Present(false)
	}
	return value, absent
}

func (u ExemplarInput) Validate() error {
	if !exemplarID(u.ID) {
		return ErrInvalid
	}
	return validateExemplar(u)
}
