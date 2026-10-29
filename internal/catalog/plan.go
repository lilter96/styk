package catalog

import (
	"fmt"
	"sort"
)

type Authority string

const (
	MoySkladAuthoritative Authority = "MOYSKLAD_AUTHORITATIVE"
	OzonAuthoritative     Authority = "OZON_AUTHORITATIVE"
	Manual                Authority = "MANUAL"
	Ignore                Authority = "IGNORE"
)

// Policies permits exactly one authority for each field.
type Policies map[Field]Authority

type Mapping struct {
	ID        string   `json:"id"`
	Version   int64    `json:"version"`
	Source    Identity `json:"source"`
	Target    Identity `json:"target"`
	Confirmed bool     `json:"confirmed"`
}

// Baseline stores both observations after verified synchronization, in mapping order.
type Baseline struct {
	Source map[Field]string `json:"source"`
	Target map[Field]string `json:"target"`
}

type PlanRequest struct {
	Mapping      Mapping         `json:"mapping"`
	Source       Product         `json:"source"`
	Target       Product         `json:"target"`
	Policies     Policies        `json:"policies"`
	Baseline     *Baseline       `json:"baseline,omitempty"`
	ManualValues map[Field]Value `json:"manual_values,omitempty"`
}

type Change struct {
	Field             Field    `json:"field"`
	Source            Identity `json:"source"`
	Target            Identity `json:"target"`
	Before            Value    `json:"before"`
	After             Value    `json:"after"`
	SourceFingerprint string   `json:"source_fingerprint"`
	TargetFingerprint string   `json:"target_fingerprint"`
}

type Conflict struct {
	Field             Field    `json:"field"`
	Source            Identity `json:"source"`
	Target            Identity `json:"target"`
	Reason            string   `json:"reason"`
	SourceValue       Value    `json:"source_value"`
	TargetValue       Value    `json:"target_value"`
	BaselineSource    string   `json:"baseline_source"`
	BaselineTarget    string   `json:"baseline_target"`
	SourceFingerprint string   `json:"source_fingerprint"`
	TargetFingerprint string   `json:"target_fingerprint"`
}

type SkippedField struct {
	Field  Field  `json:"field"`
	Reason string `json:"reason"`
}

type Plan struct {
	MappingID      string         `json:"mapping_id"`
	MappingVersion int64          `json:"mapping_version"`
	Changes        []Change       `json:"changes"`
	Conflicts      []Conflict     `json:"conflicts"`
	Skipped        []SkippedField `json:"skipped"`
	Noop           bool           `json:"noop"`
	Fingerprint    string         `json:"fingerprint"`
}

// BuildPlan requires a confirmed identity pair. It performs no provider or storage I/O.
func BuildPlan(request PlanRequest) (Plan, error) {
	if err := validatePlanRequest(request); err != nil {
		return Plan{}, err
	}
	plan := newPlan(request.Mapping)
	for _, field := range policyFields(request.Policies) {
		if err := plan.addField(request, field); err != nil {
			return Plan{}, fmt.Errorf("plan field %q: %w", field, err)
		}
	}
	plan.Noop = len(plan.Changes) == 0 && len(plan.Conflicts) == 0
	fingerprint, err := fingerprintPlan(request, plan)
	if err != nil {
		return Plan{}, err
	}
	plan.Fingerprint = fingerprint
	return plan, nil
}

func newPlan(mapping Mapping) Plan {
	return Plan{MappingID: mapping.ID, MappingVersion: mapping.Version,
		Changes: make([]Change, 0), Conflicts: make([]Conflict, 0), Skipped: make([]SkippedField, 0)}
}

func validatePlanRequest(request PlanRequest) error {
	if !request.Mapping.Confirmed || !validIdentifier(request.Mapping.ID) {
		return ErrUnconfirmedMapping
	}
	if !request.Mapping.Source.SameEntity(request.Source.Identity) || !request.Mapping.Target.SameEntity(request.Target.Identity) {
		return ErrIdentityMismatch
	}
	if request.Source.Identity.Account.Provider == request.Target.Identity.Account.Provider {
		return ErrIdentityMismatch
	}
	for field, authority := range request.Policies {
		if !field.Valid() || !authority.valid() {
			return ErrInvalidPolicy
		}
	}
	return nil
}

func (authority Authority) valid() bool {
	return authority == MoySkladAuthoritative || authority == OzonAuthoritative || authority == Manual || authority == Ignore
}

func policyFields(policies Policies) []Field {
	fields := make([]Field, 0, len(policies))
	for field := range policies {
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i] < fields[j] })
	return fields
}

func (plan *Plan) addField(request PlanRequest, field Field) error {
	if request.Policies[field] == Ignore {
		plan.Skipped = append(plan.Skipped, SkippedField{Field: field, Reason: "ignored"})
		return nil
	}
	if !field.LiveWritable() {
		plan.Skipped = append(plan.Skipped, SkippedField{Field: field, Reason: "unsupported_write_field"})
		return nil
	}
	pair := fieldPairFor(request, field)
	if reason := pair.skipReason(request.Policies[field]); reason != "" {
		plan.Skipped = append(plan.Skipped, SkippedField{Field: field, Reason: reason})
		return nil
	}
	change, err := pair.change(field)
	if err != nil {
		return err
	}
	if EqualValue(change.After, change.Before) {
		return nil
	}
	if pair.conflicts(change) {
		plan.Conflicts = append(plan.Conflicts, pair.conflict(change))
		return nil
	}
	plan.Changes = append(plan.Changes, change)
	return nil
}

type fieldPair struct {
	source, target                 Product
	sourceValue, targetValue       Value
	baselineSource, baselineTarget string
}

func fieldPairFor(request PlanRequest, field Field) fieldPair {
	pair := fieldPair{source: request.Source, target: request.Target}
	if request.Policies[field] == OzonAuthoritative && pair.source.Identity.Account.Provider != ProviderOzon {
		pair.source, pair.target = pair.target, pair.source
	}
	if request.Policies[field] == MoySkladAuthoritative && pair.source.Identity.Account.Provider != ProviderMoySklad {
		pair.source, pair.target = pair.target, pair.source
	}
	pair.sourceValue, pair.targetValue = valueAt(pair.source, field), valueAt(pair.target, field)
	if request.Policies[field] == Manual {
		pair.sourceValue = manualValueAt(request.ManualValues, field)
	}
	pair.baselineSource, pair.baselineTarget = baselineAt(request, pair, field)
	return pair
}

func valueAt(product Product, field Field) Value {
	value, ok := product.Fields[field]
	if !ok {
		return Value{State: StateAbsent}
	}
	return value
}

func manualValueAt(values map[Field]Value, field Field) Value {
	value, ok := values[field]
	if !ok {
		return Value{State: StateAbsent}
	}
	return value
}

func baselineAt(request PlanRequest, pair fieldPair, field Field) (string, string) {
	if request.Baseline == nil {
		return "", ""
	}
	if pair.source.Identity.Account.Provider == request.Source.Identity.Account.Provider {
		return request.Baseline.Source[field], request.Baseline.Target[field]
	}
	return request.Baseline.Target[field], request.Baseline.Source[field]
}

func (pair fieldPair) skipReason(authority Authority) string {
	if authority == Ignore {
		return "ignored"
	}
	if pair.source.Lifecycle == LifecycleArchived || pair.target.Lifecycle == LifecycleArchived {
		return "archived_product"
	}
	if authority == Manual && pair.sourceValue.State == StateAbsent {
		return "manual_value_required"
	}
	if reason := sourceSkipReason(pair.sourceValue.State); reason != "" {
		return reason
	}
	if pair.targetValue.State == StateUnknown || pair.targetValue.State == StateUnsupported || pair.targetValue.State == StateAbsent {
		return "target_" + string(pair.targetValue.State)
	}
	return ""
}

func sourceSkipReason(state FieldState) string {
	switch state {
	case StateAbsent, StateUnknown, StateUnsupported:
		return "source_" + string(state)
	case StateEmpty:
		return "explicit_clear_required"
	default:
		return ""
	}
}

func (pair fieldPair) change(field Field) (Change, error) {
	if err := validateSelectedValues(map[Field]Value{field: pair.sourceValue}); err != nil {
		return Change{}, err
	}
	source, err := FingerprintValue(pair.sourceValue)
	if err != nil {
		return Change{}, err
	}
	target, err := FingerprintValue(pair.targetValue)
	if err != nil {
		return Change{}, err
	}
	return Change{Field: field, Source: pair.source.Identity, Target: pair.target.Identity,
		Before: cloneValue(pair.targetValue), After: cloneValue(pair.sourceValue), SourceFingerprint: source, TargetFingerprint: target}, nil
}

func cloneValue(value Value) Value {
	value.Data = append(value.Data[:0:0], value.Data...)
	return value
}

func (pair fieldPair) conflicts(change Change) bool {
	return pair.baselineSource != "" && pair.baselineTarget != "" &&
		pair.baselineSource != change.SourceFingerprint && pair.baselineTarget != change.TargetFingerprint
}

func (pair fieldPair) conflict(change Change) Conflict {
	return Conflict{Field: change.Field, Source: change.Source, Target: change.Target, Reason: "both_sides_changed",
		SourceValue: change.After, TargetValue: change.Before, BaselineSource: pair.baselineSource,
		BaselineTarget: pair.baselineTarget, SourceFingerprint: change.SourceFingerprint, TargetFingerprint: change.TargetFingerprint}
}
