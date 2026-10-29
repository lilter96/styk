package moysklad

import (
	"context"
	"sort"

	"github.com/lilter96/styk/internal/catalog"
)

// ValidateCatalog checks reviewable provider facts before a durable dispatch
// identity exists. It neither binds an intent nor sends a mutation. Dispatch
// still requires the exact persisted command and fresh final authorization.
func (c *CatalogClient) ValidateCatalog(ctx context.Context, cmd catalog.Command) ([]catalog.FieldError, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := catalogAccount(cmd.Scope, cmd.Target.Account); err != nil {
		return nil, err
	}
	if cmd.Target.EntityType != "product" && cmd.Target.EntityType != "variant" {
		return []catalog.FieldError{catalogValidationError("", "UNSUPPORTED_ENTITY_TYPE")}, nil
	}
	body := make(map[string]any, len(cmd.Values)+len(cmd.Extensions))
	errors := c.validateCatalogSelectedFields(cmd, body)
	errors = append(errors, validateCatalogSelectedExtensions(cmd, body)...)
	errors = append(errors, validateCatalogRequiredFields(cmd, body)...)
	return errors, nil
}

func (c *CatalogClient) validateCatalogSelectedFields(cmd catalog.Command, body map[string]any) []catalog.FieldError {
	fields := make([]catalog.Field, 0, len(cmd.Values))
	for field := range cmd.Values {
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i] < fields[j] })
	errors := make([]catalog.FieldError, 0)
	for _, field := range fields {
		value := cmd.Values[field]
		if catalog.ValidateValue(value) != nil || c.catalogPayloadField(body, cmd.Target.EntityType, field, value) != nil {
			errors = append(errors, catalogValidationError(field, "INVALID_CATALOG_VALUE"))
		}
	}
	return errors
}

func validateCatalogSelectedExtensions(cmd catalog.Command, body map[string]any) []catalog.FieldError {
	keys := make([]string, 0, len(cmd.Extensions))
	for key := range cmd.Extensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	errors := make([]catalog.FieldError, 0)
	for _, key := range keys {
		one := cmd
		one.Extensions = map[string]catalog.Value{key: cmd.Extensions[key]}
		if catalog.ValidateValue(cmd.Extensions[key]) != nil || catalogPayloadExtensions(body, one) != nil {
			field := map[string]catalog.Field{"moysklad.article": catalog.FieldArticle, "moysklad.characteristics": catalog.FieldAttributes}[key]
			errors = append(errors, catalogValidationError(field, "INVALID_PROVIDER_EXTENSION"))
		}
	}
	return errors
}

func validateCatalogRequiredFields(cmd catalog.Command, body map[string]any) []catalog.FieldError {
	if cmd.Action != catalog.ActionCreate && cmd.Action != catalog.ActionUpdate && cmd.Action != catalog.ActionArchive {
		return []catalog.FieldError{catalogValidationError("", "UNSUPPORTED_ACTION")}
	}
	if cmd.Action != catalog.ActionCreate {
		return nil
	}
	errors := make([]catalog.FieldError, 0, 2)
	if cmd.Target.EntityType == "product" {
		if body["name"] == nil {
			errors = append(errors, catalogValidationError(catalog.FieldName, "REQUIRED_NAME"))
		}
		if cmd.Parent != nil {
			errors = append(errors, catalogValidationError(catalog.FieldParent, "UNSUPPORTED_PRODUCT_PARENT"))
		}
		return errors
	}
	if cmd.Parent == nil || catalogIdentity(cmd.Target.Account, *cmd.Parent) != nil || cmd.Parent.EntityType != "product" {
		errors = append(errors, catalogValidationError(catalog.FieldParent, "REQUIRED_VARIANT_PARENT"))
	}
	if body["characteristics"] == nil {
		errors = append(errors, catalogValidationError(catalog.FieldAttributes, "REQUIRED_VARIANT_CHARACTERISTICS"))
	}
	return errors
}

func catalogValidationError(field catalog.Field, code string) catalog.FieldError {
	return catalog.FieldError{Field: field, Code: code, Message: "The selected catalog facts are not supported by MoySklad."}
}
