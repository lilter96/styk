package orders

import "reflect"

// AncestorTargetMatches keeps historical and non-primary ancestor matching
// strict. Only the primary owned-position reduction may preserve appended rows;
// its actual full target revision is separately frozen by the command review.
func (p SplitAncestorSettlementPermission) AncestorTargetMatches(index int, target TargetObservation) bool {
	if index < 0 || index >= len(p.Ancestors) {
		return false
	}
	ancestor := p.Ancestors[index]
	if ancestor.TargetMatches(target) {
		return true
	}
	return p.appendedPrimaryTarget(ancestor, target)
}

func (p SplitAncestorSettlementPermission) appendedPrimaryTarget(ancestor SplitAncestorNative, target TargetObservation) bool {
	expected := ancestor.Target
	if p.Validate() != nil || p.Mode != SplitAncestorReduce || ancestor.Posting.PostingNumber != p.Source.PostingNumber || p.Baseline.Correlation != expected.Correlation || target.Scope != p.Scope || target.Correlation != expected.Correlation || !expected.Complete || !target.Complete {
		return false
	}
	if !nativeDocumentPresent(expected.SourceDeleted) || !nativeDocumentPresent(target.SourceDeleted) || !validID(target.Revision) || target.ObservedAt.IsZero() || len(target.Positions) <= len(expected.Positions) || len(target.Positions) > MaxNativeOrderPointPositions {
		return false
	}
	left, right := expected.Fields, target.Fields
	left.Positions, right.Positions = nil, nil
	if !reflect.DeepEqual(left, right) || !reflect.DeepEqual(expected.RedactedFields, target.RedactedFields) {
		return false
	}
	return appendedNativePositions(expected.Positions, target.Positions, p.Baseline)
}

func appendedNativePositions(before, after []TargetPosition, baseline PositionBaseline) bool {
	original := make(map[string]TargetPosition, len(before))
	for _, position := range before {
		if !validNativePosition(position) || original[position.ID].ID != "" {
			return false
		}
		original[position.ID] = position
	}
	owned := make(map[string]bool, len(baseline.Bindings))
	for _, binding := range baseline.Bindings {
		position, exists := original[binding.Native.ID]
		if !exists || !NativePositionEqual(binding.Native, position) || owned[binding.Native.ID] {
			return false
		}
		owned[binding.Native.ID] = true
	}
	return appendedNativeRows(original, owned, after)
}

func appendedNativeRows(original map[string]TargetPosition, owned map[string]bool, after []TargetPosition) bool {
	seen := make(map[string]bool, len(after))
	added := 0
	for _, position := range after {
		if !validNativePosition(position) || position.VATEnabled.Validate() != nil || position.CompositionSafe.Validate() != nil || seen[position.ID] {
			return false
		}
		seen[position.ID] = true
		previous, exists := original[position.ID]
		if exists {
			if !NativePositionEqual(previous, position) {
				return false
			}
			continue
		}
		if owned[position.ID] {
			return false
		}
		added++
	}
	return added > 0 && len(seen)-added == len(original)
}
