// Package catalogaccess validates catalog scopes independently of ownership and dispatch guards.
package catalogaccess

import (
	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/publication"
)

// Enabled starts catalog processing; each scope still needs explicit enrollment.
func Enabled() bool {
	return publication.CatalogEnabled()
}

// Allows admits any valid connected-account scope.
// Credential resolution and transactional stores prove registration/ownership.
func Allows(scope catalog.Scope) bool {
	return publication.Catalog(scope)
}
