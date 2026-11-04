package orders

import "slices"

// RelatedPostingGroup is provider-reported assembly membership, not ancestry,
// an exhaustive split result or proof that another posting ceased to exist.
// Optional container and member presence remain independently observable.
type RelatedPostingGroup struct {
	Presence       Presence        `json:"presence"`
	PostingNumbers Field[[]string] `json:"posting_numbers"`
}

func (group RelatedPostingGroup) Validate() error {
	if group.PostingNumbers.Validate() != nil {
		return ErrInvalid
	}
	switch group.Presence {
	case PresenceNull, PresenceEmpty:
		if !group.PostingNumbers.IsMissing() {
			return ErrInvalid
		}
	case PresenceValue:
	default:
		return ErrInvalid
	}
	if !group.PostingNumbers.IsKnown() {
		return nil
	}
	numbers := *group.PostingNumbers.Value
	if len(numbers) > 1000 {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(numbers))
	for _, number := range numbers {
		if !validID(number) || len(number) > 200 || seen[number] {
			return ErrInvalid
		}
		seen[number] = true
	}
	return nil
}

// Canonical copies before sorting. Fingerprinting never rewrites the immutable
// original observation, and an absent new field preserves historical hashes.
func (group *RelatedPostingGroup) Canonical() *RelatedPostingGroup {
	if group == nil {
		return nil
	}
	result := *group
	if group.PostingNumbers.Value != nil {
		numbers := slices.Clone(*group.PostingNumbers.Value)
		slices.Sort(numbers)
		result.PostingNumbers.Value = &numbers
	}
	return &result
}
