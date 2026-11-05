// Package catalogaccess keeps the deployment catalog switch separate from
// tenant ownership, per-account configuration and durable dispatch guards.
package catalogaccess

import (
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/publication"
)

// Enabled starts catalog processing; each scope still needs explicit enrollment.
func Enabled() bool {
	return publication.CatalogEnabled()
}

// Allows admits any valid connected-account scope under the general switch.
// Credential resolution and transactional stores prove registration/ownership.
// The legacy test switch deliberately retains its original fixed scope.
func Allows(scope catalog.Scope) bool {
	return publication.Catalog(scope)
}
