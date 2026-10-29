package catalog

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidScope       = errors.New("invalid catalog account scope")
	ErrInvalidIdentity    = errors.New("invalid catalog identity")
	ErrIdentityMismatch   = errors.New("catalog identity mismatch")
	ErrInvalidCommand     = errors.New("invalid catalog command")
	ErrUnsupportedField   = errors.New("unsupported catalog field")
	ErrUnconfirmedMapping = errors.New("catalog mapping is not confirmed")
	ErrInvalidPolicy      = errors.New("invalid catalog policy")
)

func (scope Scope) Validate() error {
	if !validIdentifier(scope.Tenant) || !validIdentifier(scope.MoyskladAccount) || !validIdentifier(scope.OzonAccount) {
		return ErrInvalidScope
	}
	return nil
}

func (scope Scope) Contains(account Account) bool {
	switch account.Provider {
	case ProviderMoySklad:
		return account.ID != "" && account.ID == scope.MoyskladAccount
	case ProviderOzon:
		return account.ID != "" && account.ID == scope.OzonAccount
	default:
		return false
	}
}

func (account Account) Validate() error {
	if !account.Provider.valid() || !validIdentifier(account.ID) {
		return ErrInvalidIdentity
	}
	return nil
}

func (provider Provider) valid() bool {
	return provider == ProviderMoySklad || provider == ProviderOzon
}

func (identity Identity) Validate() error {
	if err := identity.Account.Validate(); err != nil {
		return err
	}
	if !validIdentifier(identity.ID) {
		return ErrInvalidIdentity
	}
	return nil
}

// SameEntity accepts optional provider identifiers only when they do not contradict.
func (identity Identity) SameEntity(other Identity) bool {
	if identity.Validate() != nil || other.Validate() != nil || identity.Account != other.Account || identity.ID != other.ID {
		return false
	}
	return compatibleID(identity.EntityType, other.EntityType) && compatibleID(identity.OfferID, other.OfferID) && compatibleID(identity.SKU, other.SKU)
}

func compatibleID(left, right string) bool { return left == "" || right == "" || left == right }

func validIdentifier(value string) bool {
	return strings.TrimSpace(value) != "" && !strings.ContainsRune(value, 0)
}

func (field Field) Valid() bool {
	switch field {
	case FieldName, FieldDescription, FieldArticle, FieldPrice, FieldCurrency, FieldBarcode, FieldBarcodes:
		return true
	case FieldCategory, FieldAttributes, FieldUnit, FieldImages, FieldDimensions, FieldParent, FieldLifecycle:
		return true
	default:
		return false
	}
}

// LiveWritable reports the currently supported automatic update surface.
func (field Field) LiveWritable() bool { return field == FieldName || field == FieldDescription }

func ValidateCommand(command Command) error {
	if err := validateCommandIdentity(command); err != nil {
		return err
	}
	if !validIdentifier(command.ID) {
		return ErrInvalidCommand
	}
	if err := validateAction(command); err != nil {
		return err
	}
	if err := validateSelectedValues(command.Values); err != nil {
		return err
	}
	return validateExtensions(command.Extensions)
}

func validateCommandIdentity(command Command) error {
	if err := command.Scope.Validate(); err != nil {
		return err
	}
	if err := command.Source.Validate(); err != nil {
		return err
	}
	if command.Target.Account.Validate() != nil || !command.Scope.Contains(command.Source.Account) || !command.Scope.Contains(command.Target.Account) {
		return ErrInvalidScope
	}
	if command.Source.Account.Provider == command.Target.Account.Provider {
		return ErrIdentityMismatch
	}
	if command.Action != ActionCreate {
		return command.Target.Validate()
	}
	return nil
}

func validateAction(command Command) error {
	switch command.Action {
	case ActionCreate:
		if !validIdentifier(command.IdempotencyKey) && !validIdentifier(command.CreateID) && !validIdentifier(command.CreateExternalCode) {
			return fmt.Errorf("creation needs a durable identity: %w", ErrInvalidCommand)
		}
	case ActionUpdate:
		if len(command.Values) == 0 && len(command.Extensions) == 0 {
			return ErrInvalidCommand
		}
	case ActionArchive:
	default:
		return ErrInvalidCommand
	}
	if command.Parent != nil && (!command.Scope.Contains(command.Parent.Account) || command.Parent.Account != command.Target.Account || command.Parent.Validate() != nil) {
		return ErrIdentityMismatch
	}
	return nil
}

func validateSelectedValues(values map[Field]Value) error {
	for field, value := range values {
		if !field.Valid() {
			return fmt.Errorf("field %q: %w", field, ErrUnsupportedField)
		}
		if err := validateSelectedValue(value); err != nil {
			return fmt.Errorf("field %q: %w", field, err)
		}
		if field == FieldName && value.State == StateClear {
			return fmt.Errorf("name cannot be cleared: %w", ErrInvalidValue)
		}
		if err := validateTextField(field, value); err != nil {
			return err
		}
	}
	return nil
}

func validateTextField(field Field, value Value) error {
	if value.State != StateKnown || (field != FieldName && field != FieldDescription && field != FieldArticle && field != FieldCurrency) {
		return nil
	}
	text, err := value.StringValue()
	if err != nil {
		return fmt.Errorf("field %q: %w", field, err)
	}
	if field == FieldName && strings.TrimSpace(text) == "" {
		return fmt.Errorf("blank name: %w", ErrInvalidValue)
	}
	return nil
}

func validateSelectedValue(value Value) error {
	if value.State != StateKnown && value.State != StateClear {
		return ErrInvalidValue
	}
	return ValidateValue(value)
}

func validateExtensions(values map[string]Value) error {
	for key, value := range values {
		if !validIdentifier(key) {
			return ErrUnsupportedField
		}
		if err := validateSelectedValue(value); err != nil {
			return fmt.Errorf("extension %q: %w", key, err)
		}
	}
	return nil
}
