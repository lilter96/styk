package orders

import (
	"slices"
	"time"
)

func (policy NativeDocumentPolicy) Validate() error {
	if !policy.Enabled {
		return nil
	}
	if !positiveDecimal(policy.MaximumQuantity) || !nonnegativeNotGreater(policy.MaximumQuantity, "1000") || policy.EventMaxAgeSeconds < 1 || policy.EventMaxAgeSeconds > 86400 || len(policy.Actions) == 0 || len(policy.Actions) > 2 {
		return ErrInvalid
	}
	if policy.AllowUnpostedParent && !policy.AllowPosted {
		return ErrInvalid
	}
	seen := make(map[Action]bool, len(policy.Actions))
	for _, action := range policy.Actions {
		if !action.IsNativeMovement() || seen[action] {
			return ErrInvalid
		}
		seen[action] = true
	}
	if policy.AllowPosted && (policy.AttestedAt.IsZero() || !policy.ValidUntil.After(policy.AttestedAt) || policy.ValidUntil.Sub(policy.AttestedAt) > 24*time.Hour) {
		return ErrInvalid
	}
	return nil
}

func (policy NativeDocumentPolicy) Allows(action Action, posted bool, now time.Time) bool {
	if policy.Validate() != nil || !policy.Enabled || !slices.Contains(policy.Actions, action) {
		return false
	}
	return !posted || policy.AllowPosted && !now.Before(policy.AttestedAt) && now.Before(policy.ValidUntil)
}
