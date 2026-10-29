package delivery

import (
	"slices"

	"github.com/lilter96/styk/internal/orders"
)

// WithDrift preserves a valid content identity when discovery adds evidence.
func WithDrift(profile Profile, drift orders.Drift) (Profile, error) {
	profile.Drift = append(slices.Clone(profile.Drift), drift)
	if err := profile.identify(); err != nil {
		return Profile{}, err
	}
	return profile, profile.Validate()
}
