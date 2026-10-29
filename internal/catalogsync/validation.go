package catalogsync

import (
	"fmt"
	"strings"

	"github.com/lilter96/styk/internal/catalog"
)

const maxTargets = 100

func (config Config) Validate() error {
	if err := config.Scope.Validate(); err != nil {
		return fmt.Errorf("validate scope: %w", err)
	}
	if config.Revision < 0 || config.IntervalSeconds < 30 || config.IntervalSeconds > 86400 || len(config.Targets) > maxTargets {
		return ErrInvalid
	}
	if err := validatePolicies(config.Rules); err != nil {
		return err
	}
	seen := make(map[string]bool, len(config.Targets))
	for _, enrollment := range config.Targets {
		if seen[enrollment.ID] {
			return fmt.Errorf("duplicate enrollment: %w", ErrInvalid)
		}
		seen[enrollment.ID] = true
		if err := enrollment.validate(config.Scope); err != nil {
			return fmt.Errorf("validate enrollment: %w", err)
		}
	}
	return nil
}

func validatePolicies(policies catalog.Policies) error {
	for field, authority := range policies {
		if !field.Valid() {
			return fmt.Errorf("policy field %q: %w", field, ErrInvalid)
		}
		switch authority {
		case catalog.MoySkladAuthoritative, catalog.OzonAuthoritative, catalog.Manual, catalog.Ignore:
		default:
			return fmt.Errorf("policy authority: %w", ErrInvalid)
		}
	}
	return nil
}

func (enrollment Enrollment) validate(scope catalog.Scope) error {
	if !validID(enrollment.ID) || enrollment.Source.Validate() != nil || !scope.Contains(enrollment.Source.Account) {
		return ErrInvalid
	}
	if !scope.Contains(enrollment.Target.Account) || enrollment.Source.Account.Provider == enrollment.Target.Account.Provider {
		return ErrInvalid
	}
	if err := enrollment.validateAutomatic(); err != nil {
		return err
	}
	if enrollment.Create != nil {
		return enrollment.validateCreation(scope)
	}
	if !validID(enrollment.MappingID) || enrollment.MappingVersion < 1 || enrollment.Target.Validate() != nil {
		return ErrInvalid
	}
	return nil
}

func (enrollment Enrollment) validateAutomatic() error {
	seen := make(map[catalog.Field]bool, len(enrollment.AutomaticFields))
	for _, field := range enrollment.AutomaticFields {
		if !field.LiveWritable() || seen[field] {
			return ErrInvalid
		}
		seen[field] = true
	}
	if enrollment.Automatic && (len(seen) == 0 || enrollment.Create != nil) {
		return ErrInvalid
	}
	return nil
}

func (enrollment Enrollment) validateCreation(scope catalog.Scope) error {
	create := enrollment.Create
	if enrollment.MappingID != "" || enrollment.MappingVersion != 0 || enrollment.Target.ID != "" || len(create.Values) == 0 {
		return ErrInvalid
	}
	if !validID(create.ID) && !validID(create.ExternalCode) && !validID(enrollment.Target.OfferID) {
		return ErrInvalid
	}
	command := catalog.Command{ID: "validation", Scope: scope, Source: enrollment.Source, Target: enrollment.Target,
		Action: catalog.ActionCreate, Values: create.Values, Extensions: create.Extensions, Parent: create.Parent,
		CreateID: create.ID, CreateExternalCode: create.ExternalCode, IdempotencyKey: "validation"}
	if err := catalog.ValidateCommand(command); err != nil {
		return fmt.Errorf("validate explicit creation: %w", err)
	}
	return nil
}

func validID(value string) bool {
	return value != "" && len(value) <= 256 && strings.TrimSpace(value) == value && !strings.ContainsRune(value, 0)
}

func (revisions CredentialRevisions) forAccount(account catalog.Account) int64 {
	if account.Provider == catalog.ProviderMoySklad {
		return revisions.Moysklad
	}
	if account.Provider == catalog.ProviderOzon {
		return revisions.Ozon
	}
	return 0
}

func (decision Decision) actionable() bool {
	return decision.Ready && len(decision.Validation) == 0 && len(decision.Plan.Conflicts) == 0 && len(decision.Commands) > 0
}

func (decision *Decision) block(code string) {
	decision.Ready = false
	decision.Commands = nil
	decision.Validation = append(decision.Validation, catalog.FieldError{Code: code})
}

func (enrollment Enrollment) authorizes(decision Decision) bool {
	if !enrollment.Automatic || !decision.actionable() {
		return false
	}
	allowed := make(map[catalog.Field]bool, len(enrollment.AutomaticFields))
	for _, field := range enrollment.AutomaticFields {
		allowed[field] = true
	}
	for _, command := range decision.Commands {
		if command.Action != catalog.ActionUpdate || len(command.Extensions) != 0 {
			return false
		}
		for field := range command.Values {
			if !allowed[field] {
				return false
			}
		}
	}
	return true
}
