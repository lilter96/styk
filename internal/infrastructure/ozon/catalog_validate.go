package ozon

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

type catalogValidationError struct {
	problems []catalog.FieldError
	cause    error
}

func (e *catalogValidationError) Error() string { return "ozon catalog preflight rejected the command" }
func (e *catalogValidationError) Unwrap() error { return e.cause }

// ValidateCatalog performs the same read-only schema and stale-target checks
// used before dispatch. It neither binds nor consumes a durable write intent.
func (c *CatalogClient) ValidateCatalog(ctx context.Context, command catalog.Command) ([]catalog.FieldError, error) {
	if !c.writeEnabled() {
		return catalogProblems(ErrCatalogWritesDisabled), nil
	}
	if err := c.validateCatalogCommand(command); err != nil {
		return catalogProblems(err), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, _, err := c.catalogWritePayload(catalogBudgetContext(ctx), command)
	if err != nil {
		var validation *catalogValidationError
		if errors.As(err, &validation) || errors.Is(err, catalog.ErrInvalidValue) || errors.Is(err, catalog.ErrIdentityMismatch) || errors.Is(err, ErrCatalogUnsupported) {
			return catalogProblems(err), nil
		}
		return catalogProblems(err), err
	}
	return []catalog.FieldError{}, nil
}

func catalogProblems(err error) []catalog.FieldError {
	var validation *catalogValidationError
	if errors.As(err, &validation) {
		return append([]catalog.FieldError(nil), validation.problems...)
	}
	code, message := "PREFLIGHT_FAILED", "Catalog preflight could not complete"
	switch {
	case errors.Is(err, ErrCatalogWritesDisabled):
		code, message = "CATALOG_WRITES_DISABLED", "Catalog writes are disabled for this account or intent"
	case errors.Is(err, ErrCatalogUnsupported):
		code, message = "UNSUPPORTED_OPERATION", "The requested catalog operation is unsupported"
	case errors.Is(err, catalog.ErrInvalidValue):
		code, message = "INVALID_PROVIDER_FACTS", "Explicit provider facts are missing or invalid"
	case errors.Is(err, catalog.ErrIdentityMismatch):
		code, message = "TARGET_CHANGED", "The provider target or revision changed"
	case errors.Is(err, catalog.ErrInvalidScope), errors.Is(err, catalog.ErrInvalidIdentity):
		code, message = "INVALID_SCOPE", "The requested account or identity is invalid"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code, message = "PREFLIGHT_CANCELLED", "Catalog preflight was cancelled before dispatch"
	}
	return []catalog.FieldError{{Code: code, Message: message}}
}

func catalogFactError(field catalog.Field, code, message string, cause error) error {
	return &catalogValidationError{problems: []catalog.FieldError{{Field: field, Code: code, Message: message}}, cause: cause}
}

func requiredCreateFacts(command catalog.Command) error {
	fields := []catalog.Field{catalog.FieldName, catalog.FieldPrice, catalog.FieldCurrency, catalog.FieldCategory, catalog.FieldDimensions, catalog.FieldAttributes}
	problems := make([]catalog.FieldError, 0, len(fields)+1)
	for _, field := range fields {
		if value, ok := command.Values[field]; !ok || value.State != catalog.StateKnown {
			problems = append(problems, catalog.FieldError{Field: field, Code: "REQUIRED_CREATE_FACT", Message: "An explicit provider value is required"})
		}
	}
	if value, ok := command.Extensions["vat"]; !ok || value.State != catalog.StateKnown {
		problems = append(problems, catalog.FieldError{Code: "VAT_REQUIRED", Message: "An explicit VAT value is required"})
	}
	if len(problems) != 0 {
		return &catalogValidationError{problems: problems, cause: catalog.ErrInvalidValue}
	}
	return nil
}

func missingAttributeError(attribute int64) error {
	return catalogFactError(catalog.FieldAttributes, "REQUIRED_ATTRIBUTE_MISSING", "Ozon requires explicit attribute "+strconv.FormatInt(attribute, 10), catalog.ErrInvalidValue)
}

func rejectedCatalogReceipt(commandID string, err error) catalog.Receipt {
	return catalog.Receipt{CommandID: commandID, State: catalog.ReceiptRejected, Errors: catalogProblems(err)}
}
