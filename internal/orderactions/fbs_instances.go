package orderactions

import (
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

// FBSInstanceRequirement preserves unknown requirements; absent marking or
// customs flags never become false. These types check local completeness only,
// not Ozon's unverified exemplar request grammar or regulatory code validity.
type FBSInstanceRequirement struct {
	Identity            orders.LineIdentity
	InstancesRequired   orders.Field[bool]
	MarkingRequired     orders.Field[bool]
	CountryRequired     orders.Field[bool]
	DeclarationRequired orders.Field[bool]
}

type FBSInstance struct {
	ID          string
	MarkingCode orders.Field[string]
	Country     orders.Field[string]
	Declaration orders.Field[string]
}

type FBSLineInstances struct {
	Identity  orders.LineIdentity
	Instances []FBSInstance
}

// ValidateFBSInstances binds one explicit requirement to every active source
// line and requires unique per-unit data wherever any requirement is true.
func ValidateFBSInstances(posting orders.Posting, requirements []FBSInstanceRequirement, inputs []FBSLineInstances) error {
	if len(inputs) > maxFBSLines {
		return orders.ErrInvalid
	}
	remaining, err := fbsRemainingUnits(posting)
	if err != nil {
		return err
	}
	required, err := fbsRequirements(remaining, requirements)
	if err != nil {
		return err
	}
	seenIDs, seenMarks := make(map[string]bool), make(map[string]bool)
	seenLines := make(map[string]bool, len(inputs))
	for _, input := range inputs {
		key := input.Identity.Key()
		quantity, exists := remaining[key]
		if !exists || seenLines[key] || uint64(len(input.Instances)) != quantity || quantity == 0 {
			return orders.ErrConflict
		}
		for _, instance := range input.Instances {
			if err := fbsInstanceComplete(instance, required[key], seenIDs, seenMarks); err != nil {
				return err
			}
		}
		seenLines[key] = true
	}
	return fbsMissingInstances(remaining, required, seenLines)
}

func fbsRequirements(remaining map[string]uint64, requirements []FBSInstanceRequirement) (map[string]FBSInstanceRequirement, error) {
	if len(requirements) > maxFBSLines {
		return nil, orders.ErrInvalid
	}
	result := make(map[string]FBSInstanceRequirement, len(requirements))
	for _, requirement := range requirements {
		key := requirement.Identity.Key()
		quantity, exists := remaining[key]
		if _, duplicate := result[key]; !exists || duplicate || quantity == 0 || requirement.Identity.Validate() != nil {
			return nil, orders.ErrConflict
		}
		for _, flag := range requirement.flags() {
			if flag.Validate() != nil || !flag.IsKnown() {
				return nil, orders.ErrBlocked
			}
		}
		result[key] = requirement
	}
	for key, quantity := range remaining {
		if _, exists := result[key]; quantity > 0 && !exists {
			return nil, orders.ErrBlocked
		}
	}
	return result, nil
}

func (requirement FBSInstanceRequirement) flags() []orders.Field[bool] {
	return []orders.Field[bool]{requirement.InstancesRequired, requirement.MarkingRequired, requirement.CountryRequired, requirement.DeclarationRequired}
}

func (requirement FBSInstanceRequirement) requiresInstances() bool {
	for _, flag := range requirement.flags() {
		if flag.IsKnown() && flag.Value != nil && *flag.Value {
			return true
		}
	}
	return false
}

func fbsMissingInstances(remaining map[string]uint64, requirements map[string]FBSInstanceRequirement, seen map[string]bool) error {
	for key, quantity := range remaining {
		if quantity > 0 && requirements[key].requiresInstances() && !seen[key] {
			return orders.ErrBlocked
		}
	}
	return nil
}

func fbsInstanceComplete(instance FBSInstance, requirement FBSInstanceRequirement, ids, marks map[string]bool) error {
	if !fbsText(instance.ID) || ids[instance.ID] {
		return orders.ErrConflict
	}
	fields := []orders.Field[string]{instance.MarkingCode, instance.Country, instance.Declaration}
	flags := []orders.Field[bool]{requirement.MarkingRequired, requirement.CountryRequired, requirement.DeclarationRequired}
	for index, field := range fields {
		if field.Validate() != nil {
			return orders.ErrInvalid
		}
		if field.IsKnown() && !fbsInstanceCode(*field.Value) {
			return orders.ErrInvalid
		}
		if flags[index].IsKnown() && *flags[index].Value && !field.IsKnown() {
			return orders.ErrBlocked
		}
	}
	if instance.MarkingCode.IsKnown() {
		mark := *instance.MarkingCode.Value
		if marks[mark] {
			return orders.ErrConflict
		}
		marks[mark] = true
	}
	ids[instance.ID] = true
	return nil
}

func fbsInstanceCode(value string) bool {
	return len(value) > 0 && len(value) <= 4096 && !strings.ContainsAny(value, "\x00\r\n")
}
