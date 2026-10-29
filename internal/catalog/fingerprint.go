package catalog

import (
	"encoding/json"
	"fmt"
)

// CommandFingerprint binds authorization to all persisted intent and selected facts.
func CommandFingerprint(command Command) (string, error) {
	var err error
	command.Values, err = canonicalValues(command.Values, false)
	if err != nil {
		return "", err
	}
	command.Extensions, err = canonicalValues(command.Extensions, false)
	if err != nil {
		return "", err
	}
	return fingerprintJSON(command)
}

// ProductFingerprint excludes read time, source revision and provenance to prevent loops.
func ProductFingerprint(product Product) (string, error) {
	fields, err := canonicalValues(product.Fields, true)
	if err != nil {
		return "", err
	}
	extensions, err := canonicalValues(product.Extensions, true)
	if err != nil {
		return "", err
	}
	return fingerprintJSON(struct {
		Identity   Identity         `json:"identity"`
		Parent     *Identity        `json:"parent"`
		Variant    bool             `json:"variant"`
		Lifecycle  Lifecycle        `json:"lifecycle"`
		Fields     map[Field]Value  `json:"fields"`
		Extensions map[string]Value `json:"extensions"`
	}{product.Identity, product.Parent, product.Variant, product.Lifecycle, fields, extensions})
}

func canonicalValues[K ~string](values map[K]Value, omitProvenance bool) (map[K]Value, error) {
	result := make(map[K]Value, len(values))
	for key, value := range values {
		if err := ValidateValue(value); err != nil {
			return nil, fmt.Errorf("fingerprint field %q: %w", key, err)
		}
		if value.State == StateKnown {
			data, err := canonicalJSON(value.Data)
			if err != nil {
				return nil, err
			}
			value.Data = data
		}
		if omitProvenance {
			value.Provenance = Provenance{}
		}
		result[key] = value
	}
	return result, nil
}

func fingerprintJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode catalog fingerprint: %w", err)
	}
	return digest(data), nil
}

func fingerprintPlan(request PlanRequest, plan Plan) (string, error) {
	copyPlan := plan
	copyPlan.Changes = append([]Change(nil), plan.Changes...)
	for i := range copyPlan.Changes {
		copyPlan.Changes[i].Before = Value{}
		copyPlan.Changes[i].After = Value{}
	}
	copyPlan.Conflicts = append([]Conflict(nil), plan.Conflicts...)
	for i := range copyPlan.Conflicts {
		copyPlan.Conflicts[i].SourceValue = Value{}
		copyPlan.Conflicts[i].TargetValue = Value{}
	}
	return fingerprintJSON(struct {
		Mapping        Mapping   `json:"mapping"`
		Policies       Policies  `json:"policies"`
		Baseline       *Baseline `json:"baseline"`
		SourceRevision string    `json:"source_revision"`
		TargetRevision string    `json:"target_revision"`
		Plan           Plan      `json:"plan"`
	}{request.Mapping, request.Policies, request.Baseline, request.Source.Revision, request.Target.Revision, copyPlan})
}

// NewBaseline captures provider observations; callers persist it only after verified readback.
func NewBaseline(source, target Product) (Baseline, error) {
	left, err := fingerprintFields(source.Fields)
	if err != nil {
		return Baseline{}, err
	}
	right, err := fingerprintFields(target.Fields)
	if err != nil {
		return Baseline{}, err
	}
	return Baseline{Source: left, Target: right}, nil
}

func fingerprintFields(values map[Field]Value) (map[Field]string, error) {
	result := make(map[Field]string, len(values))
	for field, value := range values {
		fingerprint, err := FingerprintValue(value)
		if err != nil {
			return nil, fmt.Errorf("baseline field %q: %w", field, err)
		}
		result[field] = fingerprint
	}
	return result, nil
}
