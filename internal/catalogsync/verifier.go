package catalogsync

import "github.com/lilter96/styk/internal/catalog"

// FieldVerifier adds provider-specific readback checks without performing I/O.
// It cannot waive missing selected facts or the shared store verification rules.
type FieldVerifier interface {
	VerifyCatalogFields(catalog.Command, catalog.Product) ([]catalog.FieldError, error)
}

func verifiedReadback(provider Provider, command catalog.Command, observed catalog.Product) bool {
	if !ReadbackMatches(command, observed) {
		return false
	}
	verifier, ok := provider.(FieldVerifier)
	if !ok {
		return true
	}
	problems, err := verifier.VerifyCatalogFields(command, observed)
	return err == nil && len(problems) == 0
}
