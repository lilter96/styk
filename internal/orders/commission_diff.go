package orders

import (
	"reflect"
	"sort"
)

// CommissionSourceDiff compares retained authoritative versions, never an
// inferred accounting adjustment. Row numbers belong to this source period.
type CommissionSourceDiff struct {
	ComponentChanges  []FinancialComponentChange `json:"component_changes,omitempty"`
	BeforeFingerprint string                     `json:"before_fingerprint"`
	AfterFingerprint  string                     `json:"after_fingerprint"`
	HeaderChanged     bool                       `json:"header_changed"`
	AddedRows         []int64                    `json:"added_rows"`
	RemovedRows       []int64                    `json:"removed_rows"`
	ChangedRows       []int64                    `json:"changed_rows"`
}

func DiffCommissionSource(before, after RealizationReport) (*CommissionSourceDiff, error) {
	if before.Scope != after.Scope || before.Period != after.Period || before.Fingerprint == "" || after.Fingerprint == "" {
		return nil, ErrInvalid
	}
	result := &CommissionSourceDiff{BeforeFingerprint: before.Fingerprint, AfterFingerprint: after.Fingerprint, AddedRows: []int64{}, RemovedRows: []int64{}, ChangedRows: []int64{}}
	previous := make(map[int64]RealizationRow, len(before.Rows))
	for _, row := range before.Rows {
		previous[row.Number] = row
	}
	for _, row := range after.Rows {
		old, found := previous[row.Number]
		if !found {
			result.AddedRows = append(result.AddedRows, row.Number)
		} else if !reflect.DeepEqual(old, row) {
			result.ChangedRows = append(result.ChangedRows, row.Number)
		}
		delete(previous, row.Number)
	}
	for id := range previous {
		result.RemovedRows = append(result.RemovedRows, id)
	}
	sort.Slice(result.AddedRows, func(i, j int) bool { return result.AddedRows[i] < result.AddedRows[j] })
	sort.Slice(result.RemovedRows, func(i, j int) bool { return result.RemovedRows[i] < result.RemovedRows[j] })
	sort.Slice(result.ChangedRows, func(i, j int) bool { return result.ChangedRows[i] < result.ChangedRows[j] })
	before.Rows, after.Rows = nil, nil
	before.Analysis, after.Analysis = nil, nil
	before.Evidence, after.Evidence = nil, nil
	before.Fingerprint, after.Fingerprint = "", ""
	before.ObservedAt = after.ObservedAt
	before.Blockers, after.Blockers = nil, nil
	result.HeaderChanged = !reflect.DeepEqual(before, after)
	return result, nil
}
